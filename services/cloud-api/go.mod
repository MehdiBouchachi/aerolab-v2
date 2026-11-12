module github.com/mehdibouchachi/aerolab-v2/services/cloud-api

go 1.24.0

require (
	github.com/mehdibouchachi/aerolab-v2/contracts/mqtt v0.0.0
	github.com/mehdibouchachi/aerolab-v2/contracts/sync v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/text v0.24.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/mehdibouchachi/aerolab-v2/contracts/mqtt => ../../contracts/mqtt

replace github.com/mehdibouchachi/aerolab-v2/contracts/sync => ../../contracts/sync
