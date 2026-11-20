#!/bin/sh
set -e
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
make dist
podman run --rm -v "$root":/src:Z registry.fedoraproject.org/fedora:44 bash -c '
set -e
dnf -y -q install rpm-build dnf-plugins-core cpio file make git
dnf -y -q builddep /src/packaging/quill.spec
mkdir -p /root/rpmbuild/SOURCES
cp /src/dist/pkg/quill-*.tar.gz /root/rpmbuild/SOURCES/
rpmbuild -bb /src/packaging/quill.spec
rm -f /src/dist/pkg/quill-*.x86_64.rpm
cp /root/rpmbuild/RPMS/x86_64/*.rpm /src/dist/pkg/
curl -fsSL -o /usr/local/bin/appimagetool https://github.com/AppImage/appimagetool/releases/download/1.9.0/appimagetool-x86_64.AppImage
chmod +x /usr/local/bin/appimagetool
git config --global --add safe.directory /src
make -C /src appimage
'
version=$(sed -n 's/^Version: *//p' packaging/quill.spec)
rel="dist/release-$version"
rm -rf "$rel" && mkdir -p "$rel"
cp dist/pkg/*"$version"* "$rel"/
for apk in ../quill-android/dist/*.apk; do [ -f "$apk" ] && cp "$apk" "$rel"/; done
go run ./cmd/quill-sign -sign "$rel"
ls -la "$rel"
