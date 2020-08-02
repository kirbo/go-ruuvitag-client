package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/kirbo/go-ruuvitag/internal/models"
	"github.com/lib/pq"
)

type Store interface {
	InsertDevice(device *models.Device)
}

type dbStore struct {
	db *sql.DB
}

func (store *dbStore) insertRow(tblName, timestamp, tagId, metric string, value float32) {
	query := fmt.Sprintf(`INSERT INTO %s ("time", "tagId", "metric", "value") VALUES ('%s', '%s', '%s', '%v')`, tblName, timestamp, tagId, metric, value)
	_, err := store.db.Exec(query)
	if err != nil {
		panic(err)
	}
}

func (store *dbStore) InsertDevice(device *models.Device) {
	metricsTable := os.Getenv("POSTGRES_RUUVITAG_TABLE_METRICS")
	timestamp := time.Unix(int64(time.Duration(device.Timestamp/1000)), 0).Format(time.RFC3339)
	tagId := device.ID

	tblName := pq.QuoteIdentifier(metricsTable)

	store.insertRow(tblName, timestamp, tagId, "temperature", float32(device.Temperature))
	store.insertRow(tblName, timestamp, tagId, "humidity", float32(device.Humidity))
	store.insertRow(tblName, timestamp, tagId, "pressure", float32(device.Pressure))
	store.insertRow(tblName, timestamp, tagId, "battery", float32(device.Battery))
	store.insertRow(tblName, timestamp, tagId, "ping", float32(device.Ping))
}

var store Store

func InitStore(s Store) {
	store = s
}
