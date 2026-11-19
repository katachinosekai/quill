srpm:
	dnf -y install git golang rpm-build
	git config --global --add safe.directory '*'
	$(MAKE) -f Makefile srpm outdir=$(outdir)
