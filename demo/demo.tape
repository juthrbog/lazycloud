# The README demo. Record it with "task demo", which runs VHS in Docker
# against LocalStack with the seed data (task localstack:seed).

Output demo/demo.gif

Set Shell "bash"
Set FontSize 16
Set Width 1500
Set Height 820
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 80ms
Set Framerate 24

# Start LazyCloud (not recorded). The VHS image has no Nerd Font.
Hide
Type "./demo/lazycloud --endpoint http://localhost:4566 --no-nerd-fonts"
Enter
Sleep 2s
Show

# The home screen
Sleep 2s

# Open S3 from the command bar
Type ":s3"
Sleep 600ms
Enter
Sleep 1.5s

# Open test-bucket-1, then config/
Down
Sleep 400ms
Enter
Sleep 1.5s
Enter
Sleep 1s

# Preview settings.yaml with syntax highlighting
Down 2
Sleep 400ms
Enter
Sleep 2.5s

# Close the preview and go back to the home screen
Escape
Sleep 300ms
Escape
Sleep 300ms
Escape
Sleep 300ms
Escape
Sleep 800ms

# EC2 instances with the detail panel
Type ":ec2"
Sleep 600ms
Enter
Sleep 1.5s
Type "d"
Sleep 2.5s
Escape
Sleep 300ms
Escape
Sleep 800ms

# SQS queues with message counts
Type ":sqs"
Sleep 600ms
Enter
Sleep 2.5s
