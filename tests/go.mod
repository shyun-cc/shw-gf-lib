module github.com/shyun-cc/shw-gf-lib/tests

go 1.25.12

require (
	github.com/alicebob/miniredis/v2 v2.38.0
	github.com/gogf/gf/contrib/drivers/mysql/v2 v2.10.2
	github.com/gogf/gf/contrib/drivers/pgsql/v2 v2.10.2
	github.com/gogf/gf/contrib/nosql/redis/v2 v2.10.2
	github.com/gogf/gf/v2 v2.10.2
	github.com/google/uuid v1.6.0
	github.com/rabbitmq/amqp091-go v1.14.0
	github.com/shyun-cc/shw-gf-lib v0.0.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/BurntSushi/toml v1.5.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clbanning/mxj/v2 v2.7.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/emirpasic/gods/v2 v2.0.0-alpha // indirect
	github.com/emmansun/gmsm v0.44.1 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/grokify/html-strip-tags-go v0.1.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.23 // indirect
	github.com/mattn/go-runewidth v0.0.16 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/olekukonko/errors v1.1.0 // indirect
	github.com/olekukonko/ll v0.0.9 // indirect
	github.com/olekukonko/tablewriter v1.1.0 // indirect
	github.com/pganalyze/pg_query_go/v6 v6.2.5 // indirect
	github.com/pressly/goose/v3 v3.27.3 // indirect
	github.com/redis/go-redis/v9 v9.12.1 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.44.0 // indirect
	go.opentelemetry.io/otel/metric v1.44.0 // indirect
	go.opentelemetry.io/otel/sdk v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.44.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.54.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.40.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// 独立 module 引用本地未发布改动；主库发版后可移除本 replace（改用远端版本）。
replace github.com/shyun-cc/shw-gf-lib => ../
