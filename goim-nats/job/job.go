package job

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/liftbridge-io/go-liftbridge"
	"github.com/tsingson/discovery/naming"

	"github.com/tsingson/ex-goim/goim-nats/job/conf"
	"github.com/tsingson/ex-goim/goim-nats/job/grpc-client"

	liftprpc "github.com/liftbridge-io/go-liftbridge/liftbridge-grpc"

	pb "github.com/tsingson/ex-goim/api/logic/grpc"

	log "github.com/tsingson/zaplogger"
)

// NatsJob is push job.
type Job struct {
	c            *conf.JobConfig
	consumer     liftbridge.Client
	cometServers map[string]*grpc_client.Comet
	rooms        map[string]*Room
	roomsMutex   sync.RWMutex
}

type NatsJob = Job

// var natsCfg *conf.Nats
//
// func init() {
// 	natsCfg = &conf.Nats{
// 		Channel:   "channel",
// 		ChannelID: "channel-stream",
// 		Group:     "group",
// 		LiftAddr:  "localhost:9292", // address for lift-bridge
// 		NatsAddr:  "localhost:4222",
// 	}
// }

// New new a push job.
func New(cfg *conf.JobConfig) *Job {
	cl, err := newLiftClient(cfg)
	if err != nil {
		return nil
	}

	j := &NatsJob{
		c:        cfg,
		consumer: cl,
		rooms:    make(map[string]*Room),
	}
	// j.WatchComet(cfg.Discovery)
	return j
}

// WatchComet watch commet active
func (j *Job) WatchComet(c *naming.Config) {
	dis := naming.New(c)
	resolver := dis.Build("goim.comet")
	event := resolver.Watch()
	select {
	case _, ok := <-event:
		if !ok {
			panic("WatchComet init failed")
		}
		if ins, ok := resolver.Fetch(); ok {
			if err := j.newAddress(ins.Instances); err != nil {
				panic(err)
			}
			log.Infof("WatchComet init newAddress:%+v", ins)
		}
	case <-time.After(10 * time.Second):
		log.Error("WatchComet init instances timeout")
	}
	go func() {
		for {
			if _, ok := <-event; !ok {
				log.Info("WatchComet exit")
				return
			}
			ins, ok := resolver.Fetch()
			if ok {
				if err := j.newAddress(ins.Instances); err != nil {
					log.Errorf("WatchComet newAddress(%+v) error(%+v)", ins, err)
					continue
				}
				log.Infof("WatchComet change newAddress:%+v", ins)
			}
		}
	}()
}

func (j *Job) newAddress(insMap map[string][]*naming.Instance) error {
	ins := insMap[j.c.Env.Zone]
	if len(ins) == 0 {
		return fmt.Errorf("WatchComet instance is empty")
	}
	comets := map[string]*grpc_client.Comet{}
	for _, in := range ins {
		if old, ok := j.cometServers[in.Hostname]; ok {
			comets[in.Hostname] = old
			continue
		}
		c, err := grpc_client.NewComet(in, j.c.Comet)
		if err != nil {
			log.Errorf("WatchComet NewComet(%+v) error(%v)", in, err)
			return err
		}
		comets[in.Hostname] = c
		log.Infof("WatchComet AddComet grpc:%+v", in)
	}
	for key, old := range j.cometServers {
		if _, ok := comets[key]; !ok {
			old.Cancel()
			log.Infof("WatchComet DelComet:%s", key)
		}
	}
	j.cometServers = comets
	return nil
}

// newLiftClient  new liftbridge client
func newLiftClient(cfg *conf.JobConfig) (liftbridge.Client, error) {
	// liftAddr := "localhost:9292" // address for lift-bridge
	return liftbridge.Connect([]string{cfg.Nats.LiftAddr})
}

// Subscribe  get message
func (d *Job) Subscribe(channel, channelID string) error {
	ctx := context.Background()
	if err := d.consumer.Subscribe(ctx, channel, channelID, func(msg *liftprpc.Message, err error) {
		if err != nil {
			return
		}
		log.Info(msg.Offset, "--> ", string(msg.Value))
	}); err != nil {
		return err
	}

	<-ctx.Done()
	return nil
}

// Consume messages, watch signals
func (j *Job) Consume() {
	ctx := context.Background()

	// process push message
	pushMsg := new(pb.PushMsg)

	if err := j.consumer.Subscribe(ctx, j.c.Nats.Channel, j.c.Nats.ChannelID, func(msg *liftprpc.Message, err error) {
		if err != nil {
			return
		}
		log.Info(msg.Offset, "------------> ", string(msg.Value))

		if err := proto.Unmarshal(msg.Value, pushMsg); err != nil {
			log.Errorf("proto.Unmarshal(%v) error(%v)", msg, err)
			return
		}
		if err := j.push(context.Background(), pushMsg); err != nil {
			log.Errorf("j.push(%v) error(%v)", pushMsg, err)
		}
		log.Infof("consume: %d  %s \t%+v", msg.Offset, msg.Key, pushMsg)

	}); err != nil {
		return
	}

	<-ctx.Done()
	return

}

// ConsumeCheck messages, watch signals
func (j *Job) ConsumeCheck() {
	ctx := context.Background()
	if err := j.consumer.Subscribe(ctx, j.c.Nats.Channel, j.c.Nats.ChannelID, func(msg *liftprpc.Message, err error) {
		if err != nil {
			return
		}
		log.Info(msg.Offset, "------------> ", string(msg.Value))

		// process push message
		pushMsg := new(pb.PushMsg)

		if err := proto.Unmarshal(msg.Value, pushMsg); err != nil {
			log.Errorf("proto.Unmarshal(%v) error(%v)", msg, err)
			return
		}
		// if err := j.push(context.Background(), pushMsg); err != nil {
		// 	log.Errorf("j.push(%v) error(%v)", pushMsg, err)
		// }
		log.Infof("consume: %d  %s \t%+v", msg.Offset, msg.Key, pushMsg)

	}); err != nil {
		return
	}

	<-ctx.Done()
	return

}

// Close close resounces.
func (j *Job) Close() error {
	if j.consumer != nil {
		return j.consumer.Close()
	}
	return nil
}
