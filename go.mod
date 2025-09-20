module image-thumbnailer

go 1.22

require (
	github.com/disintegration/imaging v1.6.2
	github.com/joho/godotenv v1.5.1
	github.com/nats-io/nats.go v1.36.0
	github.com/tendant/simple-content v0.0.0-00010101000000-000000000000
	github.com/tendant/simple-process v0.0.0-00010101000000-000000000000
)

replace github.com/tendant/simple-content => github.com/tendant/simple-content latest
replace github.com/tendant/simple-process => github.com/tendant/simple-process latest
