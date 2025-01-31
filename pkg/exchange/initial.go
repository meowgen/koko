package exchange

import (
	"net"
	"strings"

	"github.com/meowgen/koko/pkg/config"
	"github.com/meowgen/koko/pkg/logger"
)

var manager RoomManager

func Initial() {
	conf := config.GetConf()
	var (
		err error
	)

	switch strings.ToLower(conf.ShareRoomType) {
	case "redis":
		manager, err = newRedisManager(Config{
			Addr:     net.JoinHostPort(conf.RedisHost, conf.RedisPort),
			Password: conf.RedisPassword,
			Clusters: conf.RedisClusters,
			DBIndex:  conf.RedisDBIndex,
		})

	default:
		manager = newLocalManager()
	}
	logger.Infof("Exchange share room type: %s", conf.ShareRoomType)
	if err != nil {
		logger.Fatal(err)
	}
}

func Register(r *Room) {
	manager.Add(r)
}

func UnRegister(r *Room) {
	manager.Delete(r)
}

func GetRoom(roomId string) *Room {
	return manager.Get(roomId)
}
