package main

import (
	"encoding/json"
	"fmt"
	"github.com/sirupsen/logrus"
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
				_, err := router.gateway.SMPPServer.findSmppSession(msg.To)
				if err != nil {
					if msg.Delivery != nil {
						logf.Level = logrus.WarnLevel
						logf.Error = fmt.Errorf("rejected message due to session being unavailable")
						logf.Print()
						err := msg.Delivery.Reject(true)
						if err != nil {
							continue
						}
					}
					logf.Level = logrus.ErrorLevel
					logf.Error = fmt.Errorf("error finding SMPP session: %v", err)
					logf.Print()
					// todo maybe to add to queue via postgres?
				} else {
					err := router.gateway.SMPPServer.sendSMPP(msg)
					if err != nil {
						// log failed to send?
						if msg.Delivery != nil {
							err := msg.Delivery.Nack(false, true)
							if err != nil {
								continue
							}
						}
						continue
					}
					if msg.Delivery == nil {
						marshal, err := json.Marshal(msg)
						if err != nil {
							// todo
							continue
						}
						err = router.gateway.AMPQClient.Publish("to_client", marshal)
						continue
					} else {
						err := msg.Delivery.Ack(false)
						if err != nil {
							continue
						}
					}
				}
				continue
			}

			carrier, _ := router.gateway.getClientCarrier(msg.From)
			if carrier != "" {
				marshal, err := json.Marshal(msg)
				if err != nil {
					// todo
					continue
				}
				// add to outbound carrier queue
				err = router.gateway.AMPQClient.Publish("to_carrier", marshal)
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
					err = router.gateway.AMPQClient.Publish("to_carrier", marshal)
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
