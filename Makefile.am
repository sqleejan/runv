export GOPATH:=$(abs_top_srcdir)/Godeps/_workspace:$(GOPATH)
if WITH_XEN
XEN_BUILD_TAG=with_xen
else
XEN_BUILD_TAG=
endif

if WITH_LIBVIRT
LIBVIRT_BUILD_TAG=with_libvirt
else
LIBVIRT_BUILD_TAG=
endif

HYPER_BULD_TAGS=$(XEN_BUILD_TAG) $(LIBVIRT_BUILD_TAG)
all-local: build-runv build-containerd
clean-local:
	-rm -f runv
install-exec-local: 
	$(INSTALL_PROGRAM) runv $(bindir)

build-runv:
	go build -tags "static_build $(HYPER_BULD_TAGS)" -o runv .
build-containerd:
	go build -tags "static_build $(HYPER_BULD_TAGS)" -o runv-containerd ./containerd/
