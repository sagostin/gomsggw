package main

import (
	"encoding/json"
	"github.com/sirupsen/logrus"
)

// CarrierRouter inbound from carriers, or in the "from_carrier" channel.
// the inbound function on the carrier processors, simply pushes the msg to the rabbitmq, and
// then this thing picks it up, for outbound, it's just the reverse.
func (router *Router) CarrierRouter() {
	for {
		var lm = router.gateway.LogManager

		msg := <-router.CarrierMsgChan

		to, _ := FormatToE164(msg.To)
		msg.To = to
		from, _ := FormatToE164(msg.From)
		msg.From = from

		switch msgType := msg.Type; msgType {
		case MsgQueueItemType.SMS:
			client, _ := router.findClientByNumber(msg.To)
			if client != nil {
				session, err := router.gateway.SMPPServer.findSmppSession(msg.To)
				if err != nil {
					if msg.Delivery != nil {
						lm.SendLog(lm.BuildLog(
							"Router.Carrier.SMS",
							"RouterFindSMPP",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": client.Username,
								"logID":  msg.LogID,
							}, err,
						))

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
					lm.SendLog(lm.BuildLog(
						"Router.Carrier.SMS",
						"RouterFindSMPP",
						logrus.ErrorLevel,
						map[string]interface{}{
							"client": client.Username,
							"logID":  msg.LogID,
						}, err,
					))
					// todo maybe to add to queue via postgres?
					continue
				}
				if session != nil {
					err := router.gateway.SMPPServer.sendSMPP(msg)
					if err != nil {
						if msg.Delivery != nil {
							err = msg.Delivery.Ack(false)
						}
						lm.SendLog(lm.BuildLog(
							"Router.Carrier.SMS",
							"RouterSendSMPP",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": client.Username,
								"logID":  msg.LogID,
							}, err,
						))
						marshal, _ := json.Marshal(msg)
						_ = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					} else {
						router.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem: msg,
							Carrier:      "inbound",
							ClientID:     client.ID,
							Internal:     false,
						}
						if msg.Delivery != nil {
							err := msg.Delivery.Ack(false)
							if err != nil {
								continue
							}
						}
						continue
					}
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

			client, _ = router.findClientByNumber(msg.From)
			if client != nil {
				// todo log & error invalid sender number
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
						lm.SendLog(lm.BuildLog(
							"Router.Carrier.SMS",
							"RouterSendCarrier",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": client.Username,
								"logID":  msg.LogID,
							}, err,
						))
						continue
					}
					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: msg,
						Carrier:      carrier,
						ClientID:     client.ID,
						Internal:     false,
					}
					if msg.Delivery != nil {
						err = msg.Delivery.Ack(false)
					}
					continue
				}
			} else {
				lm.SendLog(lm.BuildLog(
					"Router.Carrier.MMS",
					"RouterFindCarrier",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  msg.LogID,
					},
				))
			}
			lm.SendLog(lm.BuildLog(
				"Router.Carrier.SMS",
				"RouterSendFailed",
				logrus.ErrorLevel,
				map[string]interface{}{
					"client": client.Username,
					"logID":  msg.LogID,
				},
			))
			continue
		case MsgQueueItemType.MMS:
			client, _ := router.findClientByNumber(msg.To)

			if msg.Files == nil {
				if msg.Delivery != nil {
					err := msg.Delivery.Reject(false)
					if err != nil {
						continue
					}
				}

				lm.SendLog(lm.BuildLog(
					"Router.Carrier.MMS",
					"NoFiles",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  msg.LogID,
					},
				))
			}

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
					lm.SendLog(lm.BuildLog(
						"Router.Carrier.MMS",
						"RouterSendMM4",
						logrus.ErrorLevel,
						map[string]interface{}{
							"client": client.Username,
							"logID":  msg.LogID,
						}, err,
					))
					// todo maybe to add to queue via postgres?
					continue
				}
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: msg,
					Carrier:      "inbound",
					ClientID:     client.ID,
					Internal:     false,
				}
				if msg.Delivery != nil {
					err := msg.Delivery.Ack(false)
					if err != nil {
						continue
					}
				}
				continue
			}

			client, _ = router.findClientByNumber(msg.From)
			if client != nil {
				// todo log & error invalid sender number

			}

			carrier, _ := router.gateway.getClientCarrier(msg.From)
			if carrier != "" {
				// add to outbound carrier queue
				route := router.gateway.Router.findRouteByName("carrier", carrier)
				if route != nil {
					err := route.Handler.SendMMS(&msg)
					if err != nil {
						lm.SendLog(lm.BuildLog(
							"Router.Carrier.MMS",
							"RouterSendCarrier",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": client.Username,
								"logID":  msg.LogID,
							}, err,
						))

						if msg.Delivery != nil {
							err := msg.Delivery.Reject(true)
							if err != nil {
								continue
							}
						}
						continue
					}
					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: msg,
						Carrier:      carrier,
						ClientID:     client.ID,
						Internal:     false,
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
			} else {
				lm.SendLog(lm.BuildLog(
					"Router.Carrier.MMS",
					"RouterFindCarrier",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  msg.LogID,
					},
				))
			}
			// throw error?
			lm.SendLog(lm.BuildLog(
				"Router.Carrier.MMS",
				"RouterSendFailed",
				logrus.ErrorLevel,
				map[string]interface{}{
					"client": client.Username,
					"logID":  msg.LogID,
				},
			))
			continue
		}
	}
}
