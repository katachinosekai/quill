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
