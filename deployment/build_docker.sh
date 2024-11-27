rm -rf tmp
mkdir tmp

rm -rf ../frontend/dist

set -e

echo "Build"

pushd ../

./build_amd64_linux.sh

popd

cp -r ../umbrella tmp/umbrella

docker build --no-cache -t umbrella-sfu .
