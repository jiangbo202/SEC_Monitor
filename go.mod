module sec_monitor

go 1.25.0

// Pin the patched Go release locally as well as in CI. Go 1.24+ downloads it
// automatically when GOTOOLCHAIN is left at its default `auto` value.
toolchain go1.25.13

require (
	github.com/gin-gonic/gin v1.12.0
	github.com/longbridge/openapi-go v0.27.0
	github.com/robfig/cron/v3 v3.0.1
	github.com/shopspring/decimal v1.4.0
	golang.org/x/net v0.58.0
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.2
)

require (
	github.com/Allenxuxu/ringbuffer v0.0.11 // indirect
	github.com/BurntSushi/toml v1.3.2 // indirect
	github.com/Netflix/go-env v0.0.0-20220526054621-78278af1949d // indirect
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/jinzhu/copier v0.3.5 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/joho/godotenv v1.4.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/longbridge/openapi-protobufs/gen/go v0.7.0 // indirect
	github.com/longbridge/openapi-protocol/go v0.5.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/mattn/go-sqlite3 v1.14.22 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.1 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	golang.org/x/arch v0.22.0 // indirect
	golang.org/x/crypto v0.55.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
