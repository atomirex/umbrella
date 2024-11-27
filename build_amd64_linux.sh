set -e

./build_common.sh

GOOS=linux GOARCH=amd64 go build