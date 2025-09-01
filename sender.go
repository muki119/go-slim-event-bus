package seb

import "github.com/redis/go-redis/v9"

func (eventBus *StreamsEventBus) Send(stream string, message map[string]interface{}) error {
	_, err := eventBus.Connection.XAdd(eventBus.ctx, &redis.XAddArgs{
		Stream: stream,
		Values: message,
		ID:     "*",
	}).Result() // will return the string of the new entry (not currently needed ) or an error.
	if err != nil {
		return err
	}
	return nil
}
