Require go
Require python3

Set Shell "bash"
Set FontSize 18
Set Width 1440
Set Height 900
Set Padding 24
Set Theme "nord"
Set TypingSpeed 20ms
Env NO_COLOR ""
Env TERM "xterm-256color"
Env COLORTERM "truecolor"

Hide
Type `demo_dir="$(mktemp -d /tmp/rxs-vhs-reader.XXXXXX)"` Enter
Type `go build -o "$demo_dir/rxs" ./cmd/rxs` Enter
Type `printf '%s' '{"appearance":{"color_scheme":"nord"}}' > "$demo_dir/config.json"` Enter
Type `python3 -m http.server 18766 --bind 127.0.0.1 --directory . >"$demo_dir/http.log" 2>&1 &` Enter
Sleep 1s
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" add http://127.0.0.1:18766/vhs/demo-feed.xml` Enter
Type "clear" Enter
Show
Type `"$demo_dir/rxs" -db "$demo_dir/rxs.db" -config "$demo_dir/config.json"` Enter
Sleep 2s
Type "l"
Sleep 500ms
Enter
Sleep 1s
Screenshot assets/rxs-reader.png
