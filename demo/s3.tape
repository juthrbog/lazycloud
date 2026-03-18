Output demo/s3.gif

Set Shell "bash"
Set FontSize 16
Set Width 1400
Set Height 800
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 50ms
Set Framerate 30

# Setup (hidden from recording)
Hide
Type "task dev"
Sleep 5s
Show

# Select S3 (only service listed)
Down
Enter

# S3 bucket list loaded — show 3 buckets: logs-bucket, test-bucket-1, test-bucket-2
Sleep 2s

# Navigate to test-bucket-1 (cursor starts on logs-bucket)
Down 2
Sleep 300ms
Enter

# Inside the bucket — show folders and files
# Order: config/, data/, scripts/, terraform/, readme.md, test-file.txt
Sleep 2s

# Enter config/ folder to show nested navigation
Enter

# config/ contents: app.json, nginx.conf, settings.yaml
Sleep 2s

# Preview app.json content (syntax-highlighted JSON)
Enter

# Content viewer showing JSON — pause to read
Sleep 2s

# Go back to config/ listing
Escape
Sleep 500ms

# Show metadata on app.json
Type "d"
Sleep 2s
Escape

# Move to settings.yaml (down past nginx.conf)
Down
Sleep 300ms
Down
Sleep 300ms

# Preview settings.yaml
Enter

# Content viewer showing YAML — pause to read
Sleep 2s

# Go back to config/ listing
Escape
Sleep 500ms

# Show filtering
Type "/"
Type "test-file"
Sleep 2s
Escape

Down
Sleep 200ms
Down
Sleep 200ms
Space
Sleep 200ms
Down
Sleep 200ms
Space
Sleep 200ms
Down
Sleep 200ms
Space
Sleep 200ms
Type "x"
Sleep 300ms
Type "confirm"
Enter
Sleep 2s

# Open command palette
Type ":"
Sleep 2s

# Go back to home
Type "h"
Sleep 300ms
Enter

# Back into S3
Sleep 1s
Enter

Type "n"
Sleep 300ms
Type "me-make-new-bucket"
Enter
Sleep 2s

# Open event log
Shift+L

# Pause to show events
Sleep 2s

# Cancel
Escape
Sleep 1s

# Quit
Type "q"
Sleep 1s
