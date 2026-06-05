#!/bin/bash
WORKDIR="$(cd "$(dirname "$0")" && pwd)"

# Create server.sh
mkdir -p "$WORKDIR/testprograms/ticker/"
cat > "$WORKDIR/testprograms/ticker/server.sh" << 'EOF'
#!/bin/bash
count=0
while true; do
    echo "server-demo tick $count"
    count=$((count + 1))
    sleep 1
done
EOF
chmod +x "$WORKDIR/testprograms/ticker/server.sh"

# Build testprograms
echo "Building testprograms..."
go build -o "$WORKDIR/testprograms/crasher/crasher" "$WORKDIR/testprograms/crasher"
go build -o "$WORKDIR/testprograms/longrunner/longrunner" "$WORKDIR/testprograms/longrunner"
go build -o "$WORKDIR/testprograms/slowstopper/slowstopper" "$WORKDIR/testprograms/slowstopper"
go build -o "$WORKDIR/testprograms/envreporter/envreporter" "$WORKDIR/testprograms/envreporter"
echo "Done."