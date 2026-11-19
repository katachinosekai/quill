VERSION := $(shell sed -n 's/^Version: *//p' packaging/quill.spec)
DIST := dist/pkg

.PHONY: dist srpm rpm appimage clean-pkg

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
