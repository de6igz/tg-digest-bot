module billing

go 1.24.7

require (
	github.com/jackc/pgx/v5 v5.7.6
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/labstack/echo/v4 v4.12.0
	github.com/rs/zerolog v1.34.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	golang.org/x/sync v0.16.0 // indirect
	golang.org/x/text v0.28.0 // indirect
)

require (
	github.com/google/uuid v1.6.0
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/crypto v0.41.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
)

replace github.com/labstack/echo/v4 => ../third_party/github.com/labstack/echo/v4
