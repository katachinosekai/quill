#!/bin/sh
set -e
root=$(cd "$(dirname "$0")/.." && pwd)
src="$root/third_party/whisper.cpp"
out="$src/build"
if [ -f "$out/src/libwhisper.a" ] && [ "$1" != "-f" ]; then
	exit 0
fi
rm -f "$out/CMakeCache.txt"
cmake -S "$src" -B "$out" -DCMAKE_BUILD_TYPE=Release -DBUILD_SHARED_LIBS=OFF \
	-DWHISPER_BUILD_EXAMPLES=OFF -DWHISPER_BUILD_TESTS=OFF -DWHISPER_BUILD_SERVER=OFF \
	-DGGML_NATIVE=OFF -DGGML_AVX=ON -DGGML_AVX2=ON -DGGML_FMA=ON -DGGML_F16C=ON -DGGML_OPENMP=OFF >/dev/null
cmake --build "$out" -j "$(nproc)" >/dev/null
