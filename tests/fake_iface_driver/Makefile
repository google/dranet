# This Makefile is used to build the fake_iface kernel module.
obj-m := fake_iface.o

# KDIR is the path to the kernel source/headers directory.
# It's determined dynamically on the target machine using `uname -r`.
KDIR := /lib/modules/$(shell uname -r)/build
MDIR := $(CURDIR)
DESTDIR ?= $(MDIR)/build

default:
	mkdir $(DESTDIR) -p
	cp $(MDIR)/fake_iface.c $(DESTDIR)/fake_iface.c
	cp $(MDIR)/Makefile $(DESTDIR)/Makefile
	$(MAKE) -C $(KDIR) M=$(MDIR)/build modules

clean:
# 	$(MAKE) -C $(KDIR) M=$(MDIR)/build clean
	rm -rf $(DESTDIR)
