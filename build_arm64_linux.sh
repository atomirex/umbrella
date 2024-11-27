set -e

./build_common.sh

GOOS=linux GOARCH=arm64 go build