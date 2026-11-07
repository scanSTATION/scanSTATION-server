module github.com/scanSTATION/scanSTATION-server

go 1.26

require github.com/scanSTATION/lib-v370 v0.0.0

// lib-v370 is a git submodule (driver for the Perfection V37/V370).
replace github.com/scanSTATION/lib-v370 => ./lib-v370
