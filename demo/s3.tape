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
Type "export PATH=$HOME/.local/share/mise/shims:$PATH"
Enter
Sleep 500ms
Type "task localstack:up > /dev/null 2>&1 && task localstack:seed > /dev/null 2>&1"
Enter
Sleep 8s
Type "go build -o lazycloud . 2>/dev/null"
Enter
Sleep 5s
Type "clear"
Enter
Sleep 500ms
Show

# Launch LazyCloud against LocalStack (no nerd fonts)
Type "./lazycloud --endpoint http://localhost:4566 --no-nerd-fonts"

# Home screen — pause to show services list
Sleep 3s

# Navigate down to S3 Buckets and select it
Down
Sleep 500ms
Enter

# S3 bucket list loaded — pause to show
Sleep 3s

# Navigate to test-bucket-1 (past logs-bucket)
Down
Sleep 300ms
Enter

# Inside the bucket — show objects
Sleep 3s

# Preview test-file.txt content (enter)
Enter

# Content viewer showing file — pause to read
Sleep 3s

# Go back to objects
Escape
Sleep 1s

# Show metadata with d
Type "d"

# Metadata JSON displayed — pause to read
Sleep 3s

# Go back to objects
Escape
Sleep 1s

# Go back to bucket list
Escape
Sleep 1s

# Go back to home
Escape
Sleep 1s

# Show region picker
Shift+R
Sleep 2s

# Scroll down a bit
Down
Sleep 300ms
Down
Sleep 300ms
Down
Sleep 300ms

# Cancel
Escape
Sleep 1s

# Open event log
Shift+L

# Pause to show events
Sleep 3s

# Go back to home
Escape
Sleep 1s

# Open command palette
Type ":"
Sleep 2s

# Filter commands
Type "s3"
Sleep 1s

# Cancel
Escape
Sleep 1s

# Quit
Type "q"
Sleep 1s
