package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/imdario/mergo"
	_ "github.com/joho/godotenv/autoload"
	"github.com/kirbo/go-ruuvitag/internal/channels"
	"github.com/kirbo/go-ruuvitag/internal/models"
	"github.com/patrickmn/go-cache"
	"github.com/peknur/ruuvitag"
)

// Cache for variables
var (
	rdb    *redis.Client
	config models.JsonDevices

	Cache    *cache.Cache    = cache.New(0, 0)
	ctx      context.Context = context.Background()
	interval time.Time       = time.Minute
)

func connectRedis() {
	var (
		redisPassword = os.Getenv("REDIS_MASTER_PASSWORD")
		redisHost     = os.Getenv("REDIS_MASTER_HOST")
		redisPort     = os.Getenv("REDIS_MASTER_PORT")
	)

	rdb = redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%s", redisHost, redisPort),
		Password: redisPassword,
		DB:       0,
	})
}

func loadConfigs() {
	log.Print("Reloading configs...")
	jsonFile, err := os.Open("./config.json")
	if err != nil {
		fmt.Print(err)
	}

	byteValue, _ := ioutil.ReadAll(jsonFile)
	json.Unmarshal(byteValue, &config)

	for i := 0; i < len(config); i++ {
		Cache.Set(fmt.Sprintf("name:%s", config[i].ID), config[i].Name, cache.NoExpiration)
		if x, found := Cache.Get(fmt.Sprintf("%s%s", channels.Device, config[i].ID)); found {
			device := x.(models.Device)
			var (
				ping        float32 = float32(device.Ping) / 1000
				name                = device.Name
				temperature         = device.Temperature
				humidity            = device.Humidity
				pressure    float32 = float32(device.Pressure) / 100
				battery             = device.Battery
			)

			fmt.Println(fmt.Sprintf("%9.3fs ago - %-14s :: %7.2f °c, %6.2f %%H, %7.2f hPa, %5.3f v", ping, name, temperature, humidity, pressure, battery))
		}
	}
}

func startTickers() {
	ticker := time.NewTicker(interval)
	done := make(chan bool)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				loadConfigs()
				createInserts()
			}
		}
	}()
}

func createInserts() {
	for i := range config {
		sensor := &config[i]

		oldID := parseOldID(sensor.ID)

		val, err := rdb.Get(ctx, fmt.Sprintf("%s%s", channels.Device, oldID)).Result()
		if err != nil {
			log.Printf("No data found for: %s", sensor.Name)
			return
		}

		if err = setAndPublish(fmt.Sprintf("%s%v:%s", channels.Insert, makeTimestamp(), oldID), val); err != nil {
			panic(err)
		}
	}
}

func setAndPublish(channel string, data string) error {
	var err error

	err = rdb.Publish(ctx, channel, data).Err()
	if err != nil {
		return err
	}

	err = rdb.Set(ctx, channel, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func makeTimestamp() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

func parseOldID(id string) string {
	return strings.ToLower(strings.Replace(id, ":", "", -1))
}

func handler(data ruuvitag.Measurement) {
	var (
		address    = data.DeviceID()
		addressOld = parseOldID(address)
		ping       = int64(0)
		name       = ""
	)

	timestamp := makeTimestamp()
	if value, found := Cache.Get(fmt.Sprintf("lastTimestamp:%s", address)); found {
		var lastTimestamp = value.(int64)
		ping = timestamp - lastTimestamp
	}

	if value, found := Cache.Get(fmt.Sprintf("name:%s", address)); found {
		name = value.(string)
	}

	var device = models.Device{
		ID:    address,
		OldID: addressOld,
		Name:  name,
	}

	var deviceStub = models.Device{
		Ping:        ping,
		Format:      data.Format(),
		Humidity:    data.Humidity(),
		Temperature: data.Temperature(),
		Pressure:    float32(data.Pressure()) / float32(100),
		Timestamp:   timestamp,
		TimestampZ:  time.Unix(int64(time.Duration(timestamp/1000)), 0).Format(time.RFC3339),
		Acceleration: models.DeviceAcceleration{
			X: data.AccelerationX(),
			Y: data.AccelerationY(),
			Z: data.AccelerationZ(),
		},
		Battery: data.BatteryVoltage(),
	}

	if err := mergo.Merge(&device, deviceStub); err != nil {
		panic(err)
	}

	Cache.Set(fmt.Sprintf("%s%s", channels.Device, address), device, cache.NoExpiration)
	Cache.Set(fmt.Sprintf("lastTimestamp:%s", address), timestamp, cache.NoExpiration)

	log.Printf("%s[v%d] %s : %+v", address, data.Format(), name, deviceStub)

	redisData, err := json.Marshal(device)
	if err != nil {
		panic(err)
	}

	if err = setAndPublish(fmt.Sprintf("%s%s", channels.Device, addressOld), string(redisData)); err != nil {
		panic(err)
	}
}

func main() {
	connectRedis()
	loadConfigs()
	startTickers()

	scanner, err := ruuvitag.OpenScanner(10)
	if err != nil {
		log.Fatal(err)
	}

	output := scanner.Start()
	for {
		data := <-output
		handler(data)
	}
}
