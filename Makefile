VERSION := $(shell sed -n 's/^Version: *//p' packaging/quill.spec)
DIST := dist/pkg

.PHONY: dist srpm rpm appimage clean-pkg windows windows-zip kmem-sync

dist:
	rm -rf $(DIST)/quill-$(VERSION) && mkdir -p $(DIST)
	git archive --prefix=quill-$(VERSION)/ HEAD | tar -x -C $(DIST)
	cd $(DIST)/quill-$(VERSION) && go mod vendor
	tar -C $(DIST) -czf $(DIST)/quill-$(VERSION).tar.gz quill-$(VERSION)
	rm -rf $(DIST)/quill-$(VERSION)

srpm: dist
	rpmbuild -bs packaging/quill.spec --define "_sourcedir $(CURDIR)/$(DIST)" --define "_srcrpmdir $(or $(outdir),$(CURDIR)/$(DIST))"

rpm: dist
	rpmbuild -bb packaging/quill.spec --define "_sourcedir $(CURDIR)/$(DIST)" --define "_rpmdir $(CURDIR)/$(DIST)" --define "_builddir $(CURDIR)/$(DIST)/build"

appimage:
	rm -rf $(DIST)/AppDir && mkdir -p $(DIST)/AppDir
	cd $(DIST)/AppDir && rpm2cpio ../quill-$(VERSION)-*.x86_64.rpm | cpio -idm --quiet
	cp packaging/AppRun $(DIST)/AppDir/AppRun
	cp packaging/io.github.katachinosekai.quill.desktop $(DIST)/AppDir/
	cp cmd/quill/Icon.png $(DIST)/AppDir/io.github.katachinosekai.quill.png
	ARCH=x86_64 appimagetool --appimage-extract-and-run $(DIST)/AppDir $(DIST)/Quill-$(VERSION)-x86_64.AppImage

# Windows build for running Quill Desktop on another PC (e.g. a paired PC).
# Needs the MinGW cross-compiler: sudo dnf install mingw64-gcc mingw64-winpthreads-static
# No voice input (whisper.cpp is linked for Linux only).
windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc \
		GOFLAGS=-trimpath go build -ldflags "-H windowsgui -X main.version=$(VERSION)" -o dist/quill-windows-amd64.exe ./cmd/quill

# Bundle the current kmem (KMEM_REF=tag or commit, default the kmem repo's HEAD).
# Run before every release; TestBundledKmemIsCurrent fails when it is behind.
kmem-sync:
	scripts/vendor-kmem.sh $(KMEM_REF)

# Windows zip for releases: the exe, licences and a short README. Built on a
# host with MinGW; build-packages.sh then copies it into the release (it takes
# every dist/pkg file with the version in its name) and quill-sign signs it.
windows-zip: windows
	rm -rf dist/pkg/win && mkdir -p dist/pkg/win/quill
	cp dist/quill-windows-amd64.exe dist/pkg/win/quill/quill.exe
	cp LICENSE NOTICE THIRD_PARTY_LICENSES.txt packaging/README-windows.txt dist/pkg/win/quill/
	mkdir -p dist/pkg/win/quill/kmem && cp third_party/kmem/kmem third_party/kmem/kmem-guard third_party/kmem/VERSION third_party/kmem/LICENSE third_party/kmem/NOTICE dist/pkg/win/quill/kmem/
	cd dist/pkg/win && rm -f ../quill-$(VERSION)-windows-amd64.zip && zip -qr ../quill-$(VERSION)-windows-amd64.zip quill
	rm -rf dist/pkg/win
