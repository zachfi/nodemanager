
clean:
	@rm -f *pkg.tar.zst

build:
	CARCH="aarch64" makepkg -c
	CARCH="armv7h" makepkg -c
	CARCH="x86_64" makepkg -c

package:
	makepkg --printsrcinfo > .SRCINFO


.PHONY: build
