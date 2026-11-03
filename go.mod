module counterbuild

go 1.24.0

toolchain go1.24.7

require (
	golang.org/x/image v0.31.0
	golang.org/x/text v0.29.0
)

replace (
	golang.org/x/image => ./third_party/golang.org/x/image
	golang.org/x/text => ./third_party/golang.org/x/text
)
