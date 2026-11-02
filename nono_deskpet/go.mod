module nonodeskpet

go 1.22

toolchain go1.24.7

require (
	git.gammaspectra.live/WeebDataHoarder/swf-go v0.0.0-20260724101950-e5dc0ed52adf
	github.com/icza/bitio v1.1.0
	nonovec v0.0.0
)

require (
	github.com/ulikunitz/xz v0.5.11 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	golang.org/x/image v0.18.0 // indirect
	golang.org/x/text v0.16.0 // indirect
)

replace nonovec => ../nono_deskpet_vec
