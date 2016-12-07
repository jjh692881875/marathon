/*
 * Copyright (c) 2016 TFG Co <backend@tfgco.com>
 * Author: TFG Co <backend@tfgco.com>
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy of
 * this software and associated documentation files (the "Software"), to deal in
 * the Software without restriction, including without limitation the rights to
 * use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
 * the Software, and to permit persons to whom the Software is furnished to do so,
 * subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
 * FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
 * COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
 * IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
 * CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
 */

package extensions

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samuel/go-zookeeper/zk"
	"github.com/spf13/viper"
	"github.com/topfreegames/marathon/log"
	"github.com/uber-go/zap"
)

// KafkaInfo is a struct that will be used to unmarshal the info from zk
// e.g.
//	{
//		"jmx_port": -1,
//		"timestamp": "1477940170730",
//		"endpoints": [
//			"PLAINTEXT://10.0.22.198:9940"
//		],
//		"host": "10.0.22.198",
//		"version": 2,
//		"port":9940
//	}
type KafkaInfo struct {
	JmxPort   int      `json:"jmx_port"`
	Timestamp int64    `json:"timestamp"`
	Endpoints []string `json:"endpoints"`
	Host      string   `json:"host"`
	Version   int      `json:"version"`
	Port      int      `json:"port"`
}

// ZookeeperClient is the struct that connects to Zookeeper
type ZookeeperClient struct {
	Config     *viper.Viper
	ConfigPath string
	Conn       *zk.Conn
	Logger     zap.Logger
}

var config *viper.Viper

// NewZookeeperClient creates a new client
func NewZookeeperClient(configPath string, debug bool) (*ZookeeperClient, error) {
	if configPath == "" {
		return nil, errors.New("No configPath passed to configure ZookeeperClient")
	}
	client := &ZookeeperClient{
		ConfigPath: configPath,
	}
	client.ConfigureLogger(debug)
	err := client.LoadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	client.LoadDefaults()
	err = client.ConfigureConn()
	if err != nil {
		return nil, err
	}

	return client, nil
}

// LoadDefaults sets default values for keys needed by this module
func (c *ZookeeperClient) LoadDefaults() {
	c.Config.SetDefault("workers.zookeeper.hosts", []string{"localhost:9930"})
}

//ConfigureLogger with specific level.
func (c *ZookeeperClient) ConfigureLogger(debug bool) {
	ll := zap.InfoLevel
	if debug {
		ll = zap.DebugLevel
	}
	c.Logger = zap.New(
		zap.NewJSONEncoder(),
		ll,
		zap.AddCaller(),
	)
}

//LoadConfiguration from configPath
func (c *ZookeeperClient) LoadConfiguration(configPath string) error {
	c.Config = viper.New()
	c.Config.SetConfigFile(c.ConfigPath)
	c.Config.SetConfigType("yaml")
	c.Config.SetEnvPrefix("marathon")
	c.Config.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	c.Config.AutomaticEnv()
	err := c.Config.ReadInConfig()
	if err != nil {
		return fmt.Errorf("Fatal error config file: %s \n", err)
	}
	return nil
}

//ConfigureConn to zookeeper
func (c *ZookeeperClient) ConfigureConn() error {
	zookeeperHosts := c.Config.GetStringSlice("workers.zookeeper.hosts")
	log.D(c.Logger, "Connecting to Zookeeper...", func(cm log.CM) {
		cm.Write(zap.Object("zookeeperHosts", zookeeperHosts))
	})
	zk, _, err := zk.Connect(zookeeperHosts, time.Second*10)
	if err != nil {
		log.E(c.Logger, "Failed to connected to Zookeeper.", func(cm log.CM) {
			cm.Write(
				zap.Object("zookeeperHosts", zookeeperHosts),
				zap.Error(err),
			)
		})

		return err
	}
	log.D(c.Logger, "Connected to Zookeeper successfully.", func(cm log.CM) {
		cm.Write(zap.Object("zookeeperHosts", zookeeperHosts))
	})
	c.Conn = zk
	return nil
}

//IsConnected returns if the connection has been established successfully
func (c *ZookeeperClient) IsConnected() bool {
	connState := c.Conn.State()
	return connState == zk.StateConnected || connState == zk.StateConnectedReadOnly
}

//Close the connections to zookeeper
func (c *ZookeeperClient) Close() error {
	c.Conn.Close()
	return nil
}

//GetKafkaBrokers gets a slice with the hostname of the kafka brokers
func (c *ZookeeperClient) GetKafkaBrokers() ([]string, error) {
	ids, _, err := c.Conn.Children("/brokers/ids")

	if err != nil {
		log.E(c.Logger, "Getting Kafka brokers failed.", func(cm log.CM) {
			cm.Write(zap.Error(err))
		})
		return nil, err
	}

	log.D(c.Logger, "Getting Kafka brokers succeeded.", func(cm log.CM) {
		cm.Write(zap.Object("brokerIds", ids))
	})

	var brokers []string

	for _, id := range ids {
		log.D(c.Logger, "Retrieving broker extra info...", func(cm log.CM) {
			cm.Write(zap.Object("brokerId", id))
		})

		info, _, err := c.Conn.Get(fmt.Sprintf("%s%s", "/brokers/ids/", id))
		if err != nil {
			log.E(c.Logger, "Getting Kafka broker failed.", func(cm log.CM) {
				cm.Write(zap.Object("brokerId", id))
				cm.Write(zap.Error(err))
			})

			return nil, err
		}

		var kafkaInfo KafkaInfo
		json.Unmarshal(info, &kafkaInfo)
		log.D(c.Logger, "Broker extra info retrieved successfully.", func(cm log.CM) {
			cm.Write(zap.Object("kafkaInfo", kafkaInfo))
		})
		brokers = append(brokers, fmt.Sprintf("%s:%d", kafkaInfo.Host, kafkaInfo.Port))
	}

	return brokers, nil
}
