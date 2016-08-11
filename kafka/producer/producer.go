package producer

import (
	"fmt"
	"os"

	"git.topfreegames.com/topfreegames/marathon/messages"
	"github.com/Shopify/sarama"
	"github.com/spf13/viper"
	"github.com/uber-go/zap"
)

func getLogLevel() zap.Level {
	var level = zap.WarnLevel
	var environment = os.Getenv("ENV")
	if environment == "test" {
		level = zap.FatalLevel
	}
	return level
}

// Logger is the producer logger
var Logger = zap.NewJSON(getLogLevel(), zap.AddCaller())

// Producer continuosly reads from inChan and sends the received messages to kafka
func Producer(config *viper.Viper, configRoot string, inChan <-chan *messages.KafkaMessage, doneChan <-chan struct{}) {
	saramaConfig := sarama.NewConfig()
	producer, err := sarama.NewSyncProducer(
		config.GetStringSlice(fmt.Sprintf("%s.producer.brokers", configRoot)),
		saramaConfig)
	if err != nil {
		Logger.Error("Failed to start kafka producer", zap.Error(err))
		return
	}
	defer producer.Close()

	for {
		select {
		case <-doneChan:
			return // breaks out of the for
		case msg := <-inChan:
			saramaMessage := &sarama.ProducerMessage{
				Topic: msg.Topic,
				Value: sarama.StringEncoder(msg.Message),
			}

			_, _, err = producer.SendMessage(saramaMessage)
			if err != nil {
				Logger.Error("Error sending message", zap.Error(err))
			} else {
				Logger.Info("Sent message", zap.String("topic", msg.Topic))
			}
		}
	}
}
