package main

import (
	"encoding/json"
	"fmt"
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
					// todo maybe to add to queue via postgres?
					continue
				}
				if session != nil {
					err := router.gateway.SMPPServer.sendSMPP(msg)
					if err != nil {
						if msg.Delivery != nil {
							err := msg.Delivery.Reject(true)
							if err != nil {
								continue
							}
							continue
						} else {
							marshal, err := json.Marshal(msg)
							if err != nil {
								// todo
								continue
							}
							err = router.gateway.AMPQClient.Publish("client", marshal)
							continue
						}
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
			client, _ := router.findClientByNumber(msg.To)
			if client != nil {
				err := router.gateway.MM4Server.sendMM4(msg)
				if err != nil {
					if msg.Delivery != nil {
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
				marshal, err := json.Marshal(msg)
				if err != nil {
					// todo
					continue
				}
				if msg.Delivery != nil {
					err := msg.Delivery.Ack(false)
					if err != nil {
						continue
					}
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
			if msg.Delivery != nil {
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
		}
	}
}
