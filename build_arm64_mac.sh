set -e

./build_common.sh

GOOS=darwin GOARCH=arm64 go build