# Optional firmware sources for the Docker build

Scanner firmware is never stored in this repository. During `docker build`
each selected driver's library downloads and verifies its firmware itself
(for `v370`: the vendor's website → files here → an installed Flatpak → Flathub;
see `lib-v370/scanstation/README.md`).

Put a file here only if you want the build to use it, e.g. the vendor's
`epsonscan2-bundle-6.7.82.0.x86_64.deb.tar.gz` downloaded in a browser, or
an `esfwdd.bin`. Every file is verified; a modified file is skipped. Nothing
here is committed. `scripts/build-image.sh --bundle/--firmware/--flatpak`
copies files here for you.
