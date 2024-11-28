package main

import (
	"encoding/json"
	"github.com/sirupsen/logrus"
	"time"
)

// ClientRouter starts the router that handles inbound and outbound from the sms and mms servers
func (router *Router) ClientRouter() {
	for {
		msg := <-router.ClientMsgChan
		var lm = router.gateway.LogManager

		to, _ := FormatToE164(msg.To)
		msg.To = to
		from, _ := FormatToE164(msg.From)
		msg.From = from

		switch msgType := msg.Type; msgType {
		case MsgQueueItemType.SMS:
			client, _ := router.findClientByNumber(msg.To)
			fromClient, _ := router.findClientByNumber(msg.From)
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
						msg.QueuedTimestamp = time.Now()
						err = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					}
					lm.SendLog(lm.BuildLog(
						"Router.Client.SMS",
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
					err := router.gateway.SMPPServer.sendSMPP(msg, session)
					if err != nil {
						lm.SendLog(lm.BuildLog(
							"Router.Carrier.SMS",
							"RouterSendSMPP",
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
							continue
						} else {
							marshal, err := json.Marshal(msg)
							if err != nil {
								// todo
								continue
							}
							msg.QueuedTimestamp = time.Now()
							err = router.gateway.AMPQClient.Publish("client", marshal)
							continue
						}
					} else {
						router.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem: msg,
							Carrier:      "inbound",
							ClientID:     client.ID,
							Internal:     true,
						}
						if fromClient != nil {
							router.gateway.MsgRecordChan <- MsgRecord{
								MsgQueueItem: msg,
								Carrier:      "outbound",
								ClientID:     fromClient.ID,
								Internal:     true,
							}
						}

						if msg.Delivery != nil {
							err := msg.Delivery.Ack(false)
							if err != nil {
								continue
							}
						}
					}
					continue
				} else {
					lm.SendLog(lm.BuildLog(
						"Router.Client.SMS",
						"RouterFindSMPP",
						logrus.ErrorLevel,
						map[string]interface{}{
							"client": client.Username,
							"logID":  msg.LogID,
						}, err,
					))

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
				msg.QueuedTimestamp = time.Now()
				err = router.gateway.AMPQClient.Publish("carrier", marshal)
				if err != nil {
					// todo
					continue
				}
				continue
			} else {
				lm.SendLog(lm.BuildLog(
					"Router.Client.SMS",
					"RouterFindCarrier",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  msg.LogID,
					},
				))
			}
			continue
		case MsgQueueItemType.MMS:
			client, _ := router.findClientByNumber(msg.To)
			fromClient, _ := router.findClientByNumber(msg.From)
			if client != nil {
				err := router.gateway.MM4Server.sendMM4(msg)
				if err != nil {
					// throw error?
					lm.SendLog(lm.BuildLog(
						"Router.Client.MMS",
						"RouterSendMM4",
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
					} else {
						marshal, err := json.Marshal(msg)
						if err != nil {
							// todo
							continue
						}
						msg.QueuedTimestamp = time.Now()
						err = router.gateway.AMPQClient.Publish("client", marshal)
						continue
					}
					// todo maybe to add to queue via postgres?
					continue
				}
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: msg,
					Carrier:      "inbound",
					ClientID:     client.ID,
					Internal:     true,
				}
				router.gateway.MsgRecordChan <- MsgRecord{
					MsgQueueItem: msg,
					Carrier:      "outbound",
					ClientID:     fromClient.ID,
					Internal:     true,
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
				msg.QueuedTimestamp = time.Now()
				err = router.gateway.AMPQClient.Publish("carrier", marshal)
				if err != nil {
					// todo
					continue
				}
				continue
			} else {
				lm.SendLog(lm.BuildLog(
					"Router.Client.MMS",
					"RouterFindCarrier",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  msg.LogID,
					},
				))
			}

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
				msg.QueuedTimestamp = time.Now()
				err = router.gateway.AMPQClient.Publish("client", marshal)
				continue
			}
		}
	}
}
