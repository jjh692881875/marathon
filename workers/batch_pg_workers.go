package workers

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.topfreegames.com/topfreegames/marathon/kafka/producer"
	"git.topfreegames.com/topfreegames/marathon/messages"
	"git.topfreegames.com/topfreegames/marathon/models"
	"git.topfreegames.com/topfreegames/marathon/templates"
	"git.topfreegames.com/topfreegames/marathon/util"
	"github.com/satori/go.uuid"
	"github.com/spf13/viper"
	"github.com/uber-go/zap"
)

// BatchPGWorker contains all batch pg worker configs and channels
type BatchPGWorker struct {
	ID                          uuid.UUID
	Config                      *viper.Viper
	Logger                      zap.Logger
	Notifier                    *models.Notifier
	Message                     *messages.InputMessage
	Filters                     [][]interface{}
	Modifiers                   [][]interface{}
	StartedAt                   int64
	Db                          *models.DB
	ConfigPath                  string
	RedisClient                 *util.RedisClient
	TotalTokens                 int64
	ProcessedTokens             int
	TotalPages                  int64
	ProcessedPages              int64
	BatchPGWorkerStatusDoneChan chan struct{}
	BatchPGWorkerDoneChan       chan struct{}
	PgToParserChan              chan string
	ParserDoneChan              chan struct{}
	ParserToFetcherChan         chan *messages.InputMessage
	FetcherDoneChan             chan struct{}
	FetcherToBuilderChan        chan *messages.TemplatedMessage
	BuilderDoneChan             chan struct{}
	BuilderToProducerChan       chan *messages.KafkaMessage
	ProducerDoneChan            chan struct{}
}

type parseError struct {
	Message string
}

func (e parseError) Error() string {
	return fmt.Sprintf("%v", e.Message)
}

func (worker *BatchPGWorker) createChannels() {
	worker.BatchPGWorkerStatusDoneChan = make(chan struct{}, worker.Config.GetInt("batchpgworkerstatusdonechan"))
	worker.BatchPGWorkerDoneChan = make(chan struct{}, worker.Config.GetInt("batchpgworkerdonechansize"))
	worker.PgToParserChan = make(chan string, worker.Config.GetInt("pgtoparserchansize"))
	worker.ParserDoneChan = make(chan struct{}, worker.Config.GetInt("parserdonechansize"))
	worker.ParserToFetcherChan = make(chan *messages.InputMessage, worker.Config.GetInt("parsertofetcherchansize"))
	worker.FetcherDoneChan = make(chan struct{}, worker.Config.GetInt("fetcherdonechansize"))
	worker.FetcherToBuilderChan = make(chan *messages.TemplatedMessage, worker.Config.GetInt("fetchertobuilderchansize"))
	worker.BuilderDoneChan = make(chan struct{}, worker.Config.GetInt("builderdonechansize"))
	worker.BuilderToProducerChan = make(chan *messages.KafkaMessage, worker.Config.GetInt("buildertoproducerchansize"))
	worker.ProducerDoneChan = make(chan struct{}, worker.Config.GetInt("producerdonechansize"))
}

func (worker *BatchPGWorker) connectDatabase() {
	host := worker.Config.GetString("postgres.host")
	user := worker.Config.GetString("postgres.user")
	dbName := worker.Config.GetString("postgres.dbname")
	password := worker.Config.GetString("postgres.password")
	port := worker.Config.GetInt("postgres.port")
	sslMode := worker.Config.GetString("postgres.sslMode")

	db, err := models.GetDB(worker.Logger, host, user, port, sslMode, dbName, password)

	if err != nil {
		worker.Logger.Panic(
			"Could not connect to postgres...",
			zap.String("host", host),
			zap.Int("port", port),
			zap.String("user", user),
			zap.String("dbName", dbName),
			zap.String("error", err.Error()),
		)
	}
	worker.Db = db
}

func (worker *BatchPGWorker) connectRedis() {
	redisHost := worker.Config.GetString("redis.host")
	redisPort := worker.Config.GetInt("redis.port")
	redisPass := worker.Config.GetString("redis.password")
	redisDB := worker.Config.GetInt("redis.db")
	redisMaxPoolSize := worker.Config.GetInt("redis.maxPoolSize")

	rl := worker.Logger.With(
		zap.String("host", redisHost),
		zap.Int("port", redisPort),
		zap.Int("db", redisDB),
		zap.Int("maxPoolSize", redisMaxPoolSize),
	)
	rl.Debug("Connecting to redis...")
	cli, err := util.GetRedisClient(redisHost, redisPort, redisPass, redisDB, redisMaxPoolSize, rl)
	if err != nil {
		rl.Panic(
			"Could not connect to redis...",
			zap.String("error", err.Error()),
		)
	}
	worker.RedisClient = cli
	rl.Info("Connected to redis successfully.")
}

// TODO: Set all default configs
func (worker *BatchPGWorker) setConfigurationDefaults() {
	worker.Config.SetDefault("healthcheck.workingtext", "working")
	worker.Config.SetDefault("postgres.host", "localhost")
	worker.Config.SetDefault("postgres.user", "marathon")
	worker.Config.SetDefault("postgres.dbname", "marathon")
	worker.Config.SetDefault("postgres.port", 5432)
	worker.Config.SetDefault("postgres.sslmode", "disable")
	worker.Config.SetDefault("postgres.sslmode", "disable")
	worker.Config.SetDefault("workers.modules.logger.level", "error")
	worker.Config.SetDefault("workers.modules.producers", 1)
	worker.Config.SetDefault("workers.postgres.defaults.pushexpiry", 0)
	worker.Config.SetDefault("workers.postgres.defaults.modifiers.limit", 1000)
	worker.Config.SetDefault("workers.postgres.defaults.modifiers.order", "updated_at ASC")
	worker.Config.SetDefault("workers.modules.batchpgworkerdonechan", 1)
	worker.Config.SetDefault("workers.modules.pgtoparserchan", 10000)
	worker.Config.SetDefault("workers.modules.parserdonechan", 1)
	worker.Config.SetDefault("workers.modules.parsertofetcherchan", 1000)
	worker.Config.SetDefault("workers.modules.fetcherdonechan", 1)
	worker.Config.SetDefault("workers.modules.fetchertobuilderchan", 1000)
	worker.Config.SetDefault("workers.modules.builderdonechan", 1)
	worker.Config.SetDefault("workers.modules.buildertoproducerchan", 1000)
	worker.Config.SetDefault("workers.modules.producerdonechan", 1)
}

func (worker *BatchPGWorker) loadConfiguration() {
	worker.Config.SetConfigFile(worker.ConfigPath)
	worker.Config.SetEnvPrefix("marathon")
	worker.Config.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	worker.Config.AutomaticEnv()

	if err := worker.Config.ReadInConfig(); err == nil {
		worker.Logger.Info("Loaded config file.", zap.String("configFile", worker.Config.ConfigFileUsed()))
	} else {
		panic(fmt.Sprintf("Could not load configuration file from: %s", worker.ConfigPath))
	}
}

// Configure configures the worker
func (worker *BatchPGWorker) Configure() {
	worker.ID = uuid.NewV4()
	worker.setConfigurationDefaults()
	worker.Logger = ConfigureLogger(Log{Level: "warn"}, worker.Config)
	worker.loadConfiguration()
	worker.Logger = ConfigureLogger(Log{}, worker.Config)
	worker.createChannels()
	worker.connectDatabase()
	worker.connectRedis()
}

// Start starts the workers according to the configuration and returns the workers object
func (worker *BatchPGWorker) Start() {
	requireToken := false

	worker.StartedAt = time.Now().Unix()

	worker.Logger.Info("Starting worker pipeline...")

	go worker.updateStatus(worker.BatchPGWorkerStatusDoneChan)

	// Run modules
	qtyParsers := worker.Config.GetInt("workers.modules.parsers")
	worker.Logger.Debug("Starting parser...", zap.Int("quantity", qtyParsers))
	for i := 0; i < qtyParsers; i++ {
		go templates.Parser(worker.Logger, requireToken, worker.PgToParserChan, worker.ParserToFetcherChan, worker.ParserDoneChan)
	}
	worker.Logger.Debug("Started parser...", zap.Int("quantity", qtyParsers))

	qtyFetchers := worker.Config.GetInt("workers.modules.fetchers")
	worker.Logger.Debug("Starting fetcher...", zap.Int("quantity", qtyFetchers))
	for i := 0; i < qtyFetchers; i++ {
		go templates.Fetcher(worker.Logger, worker.ParserToFetcherChan, worker.FetcherToBuilderChan, worker.FetcherDoneChan, worker.Db)
	}
	worker.Logger.Debug("Started fetcher...", zap.Int("quantity", qtyFetchers))

	qtyBuilders := worker.Config.GetInt("workers.modules.builders")
	worker.Logger.Debug("Starting builder...", zap.Int("quantity", qtyBuilders))
	for i := 0; i < qtyBuilders; i++ {
		go templates.Builder(worker.Logger, worker.Config, worker.FetcherToBuilderChan, worker.BuilderToProducerChan, worker.BuilderDoneChan)
	}
	worker.Logger.Debug("Started builder...", zap.Int("quantity", qtyBuilders))

	qtyProducers := worker.Config.GetInt("workers.modules.producers")
	worker.Logger.Debug("Starting producer...", zap.Int("quantity", qtyProducers))
	for i := 0; i < qtyProducers; i++ {
		go producer.Producer(worker.Logger, worker.Config, worker.BuilderToProducerChan, worker.ProducerDoneChan)
	}
	worker.Logger.Debug("Started producer...", zap.Int("quantity", qtyProducers))

	worker.Logger.Debug("Starting pgReader...")
	go worker.pgReader(worker.Message, worker.Filters, worker.Modifiers, worker.PgToParserChan)
	worker.Logger.Debug("Started pgReader...")

	worker.Logger.Info("Started worker pipeline...")
}

// Close stops the modules of the instance
func (worker BatchPGWorker) Close() {
	worker.Logger.Info("Stopping workers")
	// close(worker.PgToKafkaChan)
	// close(worker.DoneChan)
	worker.Logger.Info("Stopped workers")
}

// GetBatchPGWorker returns a new worker
func GetBatchPGWorker(worker *BatchPGWorker) (*BatchPGWorker, error) {
	if worker.ConfigPath == "" {
		errStr := "Invalid worker config"
		worker.Logger.Error(errStr, zap.Object("worker", worker))
		e := parseError{errStr}
		return nil, e
	}
	if worker.Config == nil {
		worker.Config = viper.New()
	}
	worker.Configure()
	return worker, nil
}

// GetStatus returns a map[string]interface{} with the current worker status
func (worker BatchPGWorker) GetStatus() map[string]interface{} {
	return map[string]interface{}{
		"notificationID":  worker.ID,
		"startedAt":       worker.StartedAt,
		"totalTokens":     worker.TotalTokens,
		"processedTokens": worker.ProcessedTokens,
		"totalPages":      worker.TotalPages,
		"processedPages":  worker.ProcessedPages,
		"message":         worker.Message,
		"filters":         worker.Filters,
	}
}

// SetStatus sets the current notification status in redis
func (worker *BatchPGWorker) SetStatus() {
	cli := worker.RedisClient.Client
	worker.Logger.Info("Set in redis", zap.String("key", worker.ID.String()))

	status := worker.GetStatus()
	byteStatus, err := json.Marshal(status)
	if err != nil {
		worker.Logger.Panic("Could not parse worker status", zap.Error(err))
	}
	strStatus := string(byteStatus)

	redisKey := strings.Join([]string{worker.Notifier.ID.String(), worker.ID.String()}, "|")
	// FIXME: What's the best TTL to set? 30 * time.Day ?
	if err = cli.Set(redisKey, strStatus, 0).Err(); err != nil {
		worker.Logger.Panic("Failed to set notification key in redis", zap.Error(err))
	}
}

func (worker *BatchPGWorker) updateStatus(doneChan <-chan struct{}) {
	worker.Logger.Info("Starting status updater")
	for {
		select {
		case <-doneChan:
			return // breaks out of the for
		default:
			worker.Logger.Debug("Update worker status")
			worker.SetStatus()
			time.Sleep(250 * time.Millisecond)
		}
	}
}

// pgReader reads from pg in batches and sends the built messages to kafka
func (worker *BatchPGWorker) pgReader(message *messages.InputMessage, filters [][]interface{},
	modifiers [][]interface{}, outChan chan<- string) (chan<- string, error) {
	l := worker.Logger.With(
		zap.Object("message", message),
		zap.Object("filters", filters),
		zap.Object("modifiers", modifiers),
	)

	limit := -1
	for _, modifier := range modifiers {
		if modifier[0] == "LIMIT" {
			limit = modifier[1].(int)
		}
	}
	if limit <= 0 {
		worker.Logger.Fatal("Limit should be greater than 0", zap.Int("limit", limit))
	}
	l = l.With(zap.Int("limit", limit))

	userTokensCount, err := models.CountUserTokensByFilters(
		worker.Db, message.App, message.Service, filters, modifiers,
	)
	if err != nil {
		l.Fatal("Error while counting tokens", zap.Error(err))
	}

	// TODO: why can't we assign this directly from `models.CountUserTokensByFilters` ?
	worker.TotalTokens = userTokensCount

	if worker.TotalTokens < int64(0) {
		l.Fatal("worker.TotalTokens lower than 0", zap.Error(err))
	}

	l = l.With(zap.Int64("worker.TotalTokens", worker.TotalTokens))

	worker.TotalTokens = userTokensCount

	pages := userTokensCount/int64(limit) + 1
	workerModifiers := [2][]interface{}{ // TODO: Should we order ? {"ORDER BY", "updated_at ASC"}
		{"LIMIT", limit},
	}

	worker.TotalPages = pages

	l = l.With(zap.Int64("worker.TotalPages", worker.TotalPages))

	for worker.ProcessedPages = int64(0); worker.ProcessedPages < worker.TotalPages; worker.ProcessedPages++ {
		l.Debug("pgRead - Read page", zap.Int64("worker.ProcessedPages", worker.ProcessedPages))

		workerModifiers[1] = []interface{}{"OFFSET", worker.ProcessedPages}
		userTokens, err := models.GetUserTokensBatchByFilters(
			worker.Db, message.App, message.Service, filters, modifiers,
		)
		if err != nil {
			l.Error(
				"Failed to get user tokens by filters and modifiers",
				zap.Error(err),
			)
			return outChan, err
		}

		l.Debug("pgRead", zap.Object("len(userTokens)", len(userTokens)))

		worker.ProcessedTokens = 0
		for worker.ProcessedTokens < len(userTokens) {
			for _, userToken := range userTokens {
				message.Token = userToken.Token
				message.Locale = userToken.Locale

				strMsg, err := json.Marshal(message)
				if err != nil {
					l.Error("Failed to marshal msg", zap.Error(err))
					return outChan, err
				}
				l.Debug("pgRead - Send message to channel", zap.String("strMsg", string(strMsg)))
				outChan <- string(strMsg)
				worker.ProcessedTokens++
			}
		}
	}
	return outChan, nil
}
