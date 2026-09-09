package main

import "testing"

func TestValidateTag(t *testing.T) {
	for _, tag := range []string{"v0.0.0", "v1.2.3", "v1.2.3-rc.1+build.001", "v1.2.3-99999999999999999999999"} {
		if err := validateTag(tag); err != nil {
			t.Errorf("%q: %v", tag, err)
		}
	}
	for _, tag := range []string{"main", "dev", "1.2.3", "V1.2.3", " v1.2.3", "v1.2.3\n", "v1", "v1.2", "v01.2.3", "v1.2.3-01", "v1.2.3+bad!", "v1.2.3+", "v1.2.3-extra..1"} {
		if err := validateTag(tag); err == nil {
			t.Errorf("accepted %q", tag)
		}
	}
}
