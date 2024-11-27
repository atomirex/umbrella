set -e

protoc -I=proto --go_out=./ proto/sfu.proto

pushd frontend
npm install
CI=true npm run build --verbose
popd