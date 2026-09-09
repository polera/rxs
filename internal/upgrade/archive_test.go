package upgrade

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTarDecompressionBudget(t *testing.T) {
	binary := tar.Header{Name: "rxs", Typeflag: tar.TypeReg, Size: 3}
	skipped := tar.Header{Name: "LICENSE", Typeflag: tar.TypeReg, Size: 1024}
	metadata := tar.Header{Name: "license", Typeflag: tar.TypeReg, Format: tar.FormatPAX, PAXRecords: map[string]string{"comment": strings.Repeat("x", 4096)}}
	gnu := tar.Header{Name: strings.Repeat("long/", 100) + "LICENSE", Typeflag: tar.TypeReg, Format: tar.FormatGNU}
	for _, test := range []struct {
		name    string
		headers []tar.Header
		tail    int
	}{
		{"binary", []tar.Header{binary}, 0},
		{"oversized skipped before", []tar.Header{skipped, binary}, 0},
		{"oversized skipped after", []tar.Header{binary, skipped}, 0},
		{"cumulative", []tar.Header{skipped, skipped, skipped, binary}, 0},
		{"pax metadata", []tar.Header{metadata, binary}, 0},
		{"gnu metadata", []tar.Header{gnu, binary}, 0},
		{"trailing data", []tar.Header{binary}, 4096},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := rawTar(t, test.headers...)
			data = append(data, bytes.Repeat([]byte{'z'}, test.tail)...)
			for _, limit := range []int64{int64(len(data)), int64(len(data) + 1)} {
				got, err := extractTarBinary(context.Background(), bytes.NewReader(data), limit)
				if err != nil || string(got) != "xxx" {
					t.Fatalf("limit %d: binary = %q, %v", limit, got, err)
				}
			}
			for _, limit := range []int64{0, 512, 1536, int64(len(data) - 1)} {
				source := bytes.NewReader(data)
				got, err := extractTarBinary(context.Background(), source, limit)
				if !errors.Is(err, errArchiveTooLarge) || got != nil {
					t.Fatalf("limit %d: binary = %q, %v", limit, got, err)
				}
				if consumed := len(data) - source.Len(); int64(consumed) > limit+1 {
					t.Fatalf("read %d bytes with limit %d", consumed, limit)
				}
			}
		})
	}
}

func TestTarTruncationAndOversizedBinary(t *testing.T) {
	data := rawTar(t, tar.Header{Name: "rxs", Typeflag: tar.TypeReg, Size: 1024})
	for _, size := range []int{1, 511, 512, 513, 1024, 1535} {
		if _, err := extractTarBinary(context.Background(), bytes.NewReader(data[:size]), int64(len(data))); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("truncation at %d: %v", size, err)
		}
	}
	var oversized bytes.Buffer
	writer := tar.NewWriter(&oversized)
	if err := writer.WriteHeader(&tar.Header{Name: "rxs", Typeflag: tar.TypeReg, Size: maxBinary + 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTarBinary(context.Background(), &oversized, maxExpandedArchive); err == nil || !strings.Contains(err.Error(), "binary in release archive is too large") {
		t.Fatalf("oversized binary error = %v", err)
	}
}

func TestGzipEntireStreamValidation(t *testing.T) {
	archive := tarGzip(t, "rxs", []byte("binary"))
	corrupted := bytes.Clone(archive)
	corrupted[len(corrupted)-8] ^= 1
	for _, test := range []struct {
		name string
		data []byte
		want error
	}{
		{"truncated trailer", archive[:len(archive)-1], io.ErrUnexpectedEOF},
		{"bad checksum", corrupted, gzip.ErrChecksum},
		{"truncated member after tar EOF", append(bytes.Clone(archive), archive[:len(archive)-1]...), io.ErrUnexpectedEOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got, err := extractBinary(context.Background(), test.data, "release.tar.gz"); !errors.Is(err, test.want) || got != nil {
				t.Fatalf("binary = %q, %v", got, err)
			}
		})
	}
	combined := append(bytes.Clone(archive), archive...)
	for _, limit := range []int64{4095, 4096} {
		reader, err := gzip.NewReader(bytes.NewReader(combined))
		if err != nil {
			t.Fatal(err)
		}
		got, err := extractTarBinary(context.Background(), reader, limit)
		_ = reader.Close()
		if limit == 4095 {
			if !errors.Is(err, errArchiveTooLarge) || got != nil {
				t.Fatalf("concatenated member exceeded limit: %q, %v", got, err)
			}
		} else if err != nil || string(got) != "binary" {
			t.Fatalf("exact concatenated limit: %q, %v", got, err)
		}
	}
}

func TestExtractionCancellation(t *testing.T) {
	for _, name := range []string{"release.tar.gz", "release.zip"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := extractBinary(ctx, nil, name); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	data := rawTar(t,
		tar.Header{Name: "LICENSE", Typeflag: tar.TypeReg, Size: 64 << 10},
		tar.Header{Name: "rxs", Typeflag: tar.TypeReg, Size: 64 << 10})
	for _, after := range []int{1024, 70 << 10, len(data)} {
		ctx, cancel := context.WithCancel(context.Background())
		source := &cancelReader{reader: bytes.NewReader(data), after: after, cancel: cancel}
		got, err := extractTarBinary(ctx, source, int64(len(data)+1))
		cancel()
		if !errors.Is(err, context.Canceled) || got != nil {
			t.Fatalf("cancel after %d: %q, %v", after, got, err)
		}
	}
}

func rawTar(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := tar.NewWriter(&data)
	for _, header := range headers {
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(bytes.Repeat([]byte{'x'}, int(header.Size))); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

type cancelReader struct {
	reader io.Reader
	after  int
	cancel context.CancelFunc
}

func (r *cancelReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.after -= n
	if r.after <= 0 {
		r.cancel()
	}
	return n, err
}
