package main

import (
	"encoding/json"
	"fmt"
	"github.com/Shopify/sarama"
	"github.com/linkedin/goavro"
    "math/rand"
	"time"
	
	"os"
	"os/signal"
	"strings"
)

var (
	brokers = []string{"127.0.0.1:9092"}
	topic   = "location"
	topics  = []string{topic}
)
type CreateEvent struct {
    Latitude float64
    Longitude float64
}

func newKafkaConfiguration() *sarama.Config {
	conf := sarama.NewConfig()
	conf.Producer.RequiredAcks = sarama.WaitForAll
	conf.Producer.Return.Successes = true
	conf.ChannelBufferSize = 1
	conf.Version = sarama.V0_10_1_0
	return conf
}

func NewCreateEvent() CreateEvent {
	event := new(CreateEvent)
	event.Latitude = rand.Float64()
	event.Longitude = rand.Float64()
	return *event
}

func newKafkaSyncProducer() sarama.SyncProducer {
	kafka, err := sarama.NewSyncProducer(brokers, newKafkaConfiguration())

	if err != nil {
		panic(err)
	}
	return kafka
}

func mainProducer() {
	kafka := newKafkaSyncProducer()
	
	for {
		event := NewCreateEvent()
		sendMsg(kafka, event)
		time.Sleep(10 * time.Second)
	}
}


func consume(topics []string, master sarama.Consumer) (chan *sarama.ConsumerMessage, chan *sarama.ConsumerError) {
	consumers := make(chan *sarama.ConsumerMessage)
	errors := make(chan *sarama.ConsumerError)
	for _, topic := range topics {
		if strings.Contains(topic, "__consumer_offsets") {
			continue
		}
		partitions, _ := master.Partitions(topic)
    // this only consumes partition no 1, you would probably want to consume all partitions
		consumer, err := master.ConsumePartition(topic, partitions[0], sarama.OffsetOldest)
		if nil != err {
			fmt.Printf("Topic %v Partitions: %v", topic, partitions)
			panic(err)
		}
		fmt.Println(" Start consuming topic ", topic)
		go func(topic string, consumer sarama.PartitionConsumer) {
			for {
				select {
				case consumerError := <-consumer.Errors():
					errors <- consumerError
					fmt.Println("consumerError: ", consumerError.Err)

				case msg := <-consumer.Messages():
					consumers <- msg
					//fmt.Println("Got message on topic ", topic, msg.Value)
				}
			}
		}(topic, consumer)
	}

	return consumers, errors
}

func sendMsg(kafka sarama.SyncProducer, event interface{}) error {
	codec, err := goavro.NewCodec(`
    {
		"type": "record",
		"name": "LongList",
		"fields" : [
			{"name": "Latitude", "type": "float"},
			{"name": "Longitude", "type": "float"}
		]
	}`)
	if err != nil {
		panic(err)
	}

	json, err := json.Marshal(event)
	textual := []byte(json)

	if err != nil {
		panic(err)
	}
	native, _, err := codec.NativeFromTextual(textual)
    if err != nil {
        fmt.Println(err)
    }

    // Convert native Go form to binary Avro data
    binary, err := codec.BinaryFromNative(nil, native)
    if err != nil {
        fmt.Println(err)
    }
	
	msgLog := &sarama.ProducerMessage{
		Topic: topic,
		Value: sarama.StringEncoder(string(binary)),
	}

	partition, offset, err := kafka.SendMessage(msgLog)
	if err != nil {
		panic(err)
	}

	fmt.Printf("Message: %+v\n", event)
	fmt.Printf("Message is stored in partition %d, offset %d\n",partition, offset)

	return nil
}

func main() {
    go mainProducer()

	config := sarama.NewConfig()
	config.ClientID = "go-kafka-consumer"
	config.Consumer.Return.Errors = true

	master, err := sarama.NewConsumer(brokers, config)

	if err != nil {
		panic(err)
	}

	defer func() {
		if err := master.Close(); err != nil {
			panic(err)
		}
	}()


	consumer, errors := consume(topics, master)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	// Count how many message processed
	msgCount := 0

	// Get signnal for finish
	doneCh := make(chan struct{})
	go func() {
		for {
			select {
			case msg := <-consumer:
				msgCount++
				codec, err := goavro.NewCodec(`
				{
					"type": "record",
					"name": "LongList",
					"fields" : [
						{"name": "Latitude", "type": "float"},
						{"name": "Longitude", "type": "float"}
					]
				}`)

				if err != nil {
					panic(err)
				}
				fmt.Println()
				native, _, err := codec.NativeFromBinary(msg.Value)
				if err != nil {
					fmt.Println(err)
				}

				// Convert native Go form to textual Avro data
				textual, err := codec.TextualFromNative(nil, native)
				if err != nil {
					fmt.Println(err)
				}
				
				fmt.Println("Received messages", string(textual))//, string(msg.Key)
				fmt.Println()
			case consumerError := <-errors:
				msgCount++
				fmt.Println("Received consumerError ", string(consumerError.Topic), string(consumerError.Partition), consumerError.Err)
				doneCh <- struct{}{}
			case <-signals:
				fmt.Println("Interrupt is detected")
				doneCh <- struct{}{}
			}
		}
	}()

	<-doneCh
	fmt.Println("Processed", msgCount, "messages")
}