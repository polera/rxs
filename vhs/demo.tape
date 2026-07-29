Output assets/rxs-demo.gif

Require go
Require python3

Set Shell "bash"
Set FontSize 18
Set Width 1200
Set Height 750
Set Padding 24
Set Theme "nord"
Set TypingSpeed 35ms
Set Framerate 20
Env NO_COLOR ""
Env TERM "xterm-256color"
Env COLORTERM "truecolor"

Hide
Type `demo_dir="$(mktemp -d /tmp/rxs-vhs-demo.XXXXXX)"` Enter
Type `go build -o "$demo_dir/rxs" ./cmd/rxs` Enter
Type `printf '%s' '{"appearance":{"color_scheme":"nord"}}' > "$demo_dir/config.json"` Enter
Type `python3 -m http.server 18767 --bind 127.0.0.1 --directory . >"$demo_dir/http.log" 2>&1 &` Enter
Sleep 1s
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" add http://127.0.0.1:18767/testdata/feeds/rss.xml` Enter
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" add http://127.0.0.1:18767/testdata/feeds/atom.xml` Enter
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" add http://127.0.0.1:18767/testdata/feeds/feed.json` Enter
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" add http://127.0.0.1:18767/vhs/demo-feed.xml` Enter
Type "clear" Enter
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" -config "$demo_dir/config.json"` Enter
Sleep 2s
Show
Sleep 2s

Type "/"
Sleep 750ms
Type "local-first"
Sleep 750ms
Enter
Sleep 1s

Type "j"
Type "j"
Sleep 1s
Type "l"
Sleep 1s
Type "j"
Sleep 750ms
Type "k"
Sleep 750ms
Enter
Sleep 2s

Ctrl+f
Sleep 1s
Ctrl+f
Sleep 2s
