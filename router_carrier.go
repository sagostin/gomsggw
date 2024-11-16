package main

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
	"strings"
)

// CarrierRouter inbound from carriers, or in the "from_carrier" channel.
// the inbound function on the carrier processors, simply pushes the msg to the rabbitmq, and
// then this thing picks it up, for outbound, it's just the reverse.
func (router *Router) CarrierRouter() {
	for {
		msg := <-router.CarrierMsgChan
		logf := LoggingFormat{Type: "carrier_router"}
		logf.AddField("logID", msg.LogID)

		switch msgType := msg.Type; msgType {
		case MsgQueueItemType.SMS:
			client, _ := router.findClientByNumber(msg.To)
			if client != nil {
				session, err := router.gateway.SMPPServer.findSmppSession(msg.To)
				if err != nil {
					if msg.Delivery != nil {
						logf.Level = logrus.WarnLevel
						logf.Error = fmt.Errorf("rejected message due to session being unavailable")
						logf.Print()
						err := msg.Delivery.Reject(true)
						if err != nil {
							continue
						}
					} else {
						marshal, err := json.Marshal(msg)
						if err != nil {
							// todo
							continue
						}
						err = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					}
					logf.Level = logrus.ErrorLevel
					logf.Error = fmt.Errorf("error finding SMPP session: %v", err)
					logf.Print()
					// todo maybe to add to queue via postgres?
					continue
				}
				if session != nil {
					err := router.gateway.SMPPServer.sendSMPP(msg)
					if err != nil {
						if msg.Delivery != nil {
							err = msg.Delivery.Ack(false)
						}
						marshal, _ := json.Marshal(msg)
						_ = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					} else {
						if msg.Delivery != nil {
							err := msg.Delivery.Ack(false)
							if err != nil {
								continue
							}
						}
					}
					continue
				} else {
					if msg.Delivery != nil {
						err := msg.Delivery.Nack(false, true)
						if err != nil {
							continue
						}
					}
					continue
				}
			}

			carrier, _ := router.gateway.getClientCarrier(msg.From)
			if carrier != "" {
				// add to outbound carrier queue
				route := router.gateway.Router.findRouteByName("carrier", carrier)
				if route != nil {
					err := route.Handler.SendSMS(&msg)
					if err != nil {
						// todo log
						if msg.Delivery != nil {
							err = msg.Delivery.Reject(true)
						}
						continue
					}
					if msg.Delivery != nil {
						err = msg.Delivery.Ack(false)
					}
				}
				continue
			}
			// throw error?
			logf.Error = fmt.Errorf("unable to send")
			logf.Print()
		case MsgQueueItemType.MMS:
			msg.To = strings.Split(msg.To, "/")[0]
			msg.From = strings.Split(msg.From, "/")[0]
			client, _ := router.findClientByNumber(msg.To)

			if msg.Files == nil {
				if msg.Delivery != nil {
					logf.Level = logrus.WarnLevel
					logf.Error = fmt.Errorf("unable to send to mm4 client")
					logf.Print()
					err := msg.Delivery.Reject(false)
					if err != nil {
						continue
					}
				}
			}

			if client != nil {
				err := router.gateway.MM4Server.sendMM4(msg)
				if err != nil {
					if msg.Delivery != nil {
						logf.Level = logrus.WarnLevel
						logf.Error = fmt.Errorf("unable to send to mm4 client")
						logf.Print()
						err := msg.Delivery.Reject(true)
						if err != nil {
							continue
						}
					} else {
						marshal, err := json.Marshal(msg)
						if err != nil {
							// todo
							continue
						}
						err = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					}
					logf.Level = logrus.ErrorLevel
					logf.Error = fmt.Errorf("error finding SMPP session, adding to queue: %v", err)
					logf.Print()
					// todo maybe to add to queue via postgres?
					continue
				}
				if msg.Delivery != nil {
					err := msg.Delivery.Ack(false)
					if err != nil {
						continue
					}
				}
				continue
			}

			carrier, _ := router.gateway.getClientCarrier(msg.From)
			if carrier != "" {
				// add to outbound carrier queue
				route := router.gateway.Router.findRouteByName("carrier", carrier)
				if route != nil {
					err := route.Handler.SendMMS(&msg)
					if err != nil {
						// todo log
						if msg.Delivery != nil {
							err := msg.Delivery.Reject(true)
							if err != nil {
								return
							}
						}
						continue
					}
					if msg.Delivery != nil {
						err := msg.Delivery.Ack(false)
						if err != nil {
							continue
						}
					}
					continue

				}
				continue
			}
			// throw error?
			logf.Error = fmt.Errorf("unable to send")
			logf.Print()
			continue
		}
	}
}
