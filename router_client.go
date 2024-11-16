package main

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
)

// ClientRouter starts the router that handles inbound and outbound from the sms and mms servers
func (router *Router) ClientRouter() {
	for {
		msg := <-router.ClientMsgChan
		logf := LoggingFormat{Type: "client_router"}
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
						marshal, err := json.Marshal(msg)
						if err != nil {
							// todo
							continue
						}
						err = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					} else {
						if msg.Delivery != nil {
							err := msg.Delivery.Ack(true)
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
				marshal, err := json.Marshal(msg)
				if err != nil {
					// todo
					continue
				}
				// add to outbound carrier queue
				err = router.gateway.AMPQClient.Publish("carrier", marshal)
				if err != nil {
					// todo
					continue
				}
				continue
			}
			// throw error?
			logf.Error = fmt.Errorf("unable to send")
			logf.Print()
		case MsgQueueItemType.MMS:
			// check by destination before assuming it needs to be sent to the carrier
			destinationAddress := router.gateway.MM4Server.getIPByRecipient(msg.To)
			if destinationAddress != "" {
				// destinationAddress not found for MM4 client, so we will skip, and grab carrier info
				// we will also try sending to the mm4 server if we can?
				/*err := router.gateway.MM4Server.sendMM4(destinationAddress, msg)
				if err != nil {
					logf.Level = logrus.ErrorLevel
					logf.Error = err
					logf.Message = fmt.Sprintf("error sending MM4 message")
					logf.Print()
				}*/
				continue
			} else {
				carrier, _ := router.gateway.getClientCarrier(msg.From)
				if carrier != "" {
					marshal, err := json.Marshal(msg)
					if err != nil {
						// todo
						continue
					}
					// add to outbound carrier queue
					err = router.gateway.AMPQClient.Publish("carrier", marshal)
					if err != nil {
						// todo
						continue
					}
					continue
				}
				// throw error?
				logf.Error = fmt.Errorf("unable to send")
				logf.Print()
			}
			println("todo: mms queue item")

		}
	}
}
