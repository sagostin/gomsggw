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

		toClient, _ := router.findClientByNumber(msg.To)
		fromClient, _ := router.findClientByNumber(msg.From)

		if fromClient == nil && toClient == nil {
			lm.SendLog(lm.BuildLog(
				"Router.Client.SMS",
				"Invalid sender number.",
				logrus.ErrorLevel,
				map[string]interface{}{
					"logID": msg.LogID,
					"from":  msg.From,
				},
			))
			continue
		}

		switch msgType := msg.Type; msgType {
		case MsgQueueItemType.SMS:
			if toClient != nil {
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
						err = router.gateway.AMPQClient.Publish("toClient", marshal)
						continue
					}
					lm.SendLog(lm.BuildLog(
						"Router.Client.SMS",
						"RouterFindSMPP",
						logrus.ErrorLevel,
						map[string]interface{}{
							"toClient": toClient.Username,
							"logID":    msg.LogID,
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
								"toClient": toClient.Username,
								"logID":    msg.LogID,
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
							err = router.gateway.AMPQClient.Publish("toClient", marshal)
							continue
						}
					} else {

						var internal = fromClient != nil && toClient != nil
						if fromClient != nil {
							router.gateway.MsgRecordChan <- MsgRecord{
								MsgQueueItem: msg,
								Carrier:      "from_client",
								ClientID:     fromClient.ID,
								Internal:     internal,
							}
						}
						if toClient != nil {
							router.gateway.MsgRecordChan <- MsgRecord{
								MsgQueueItem: msg,
								Carrier:      "to_client",
								ClientID:     toClient.ID,
								Internal:     internal,
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
							"toClient": toClient.Username,
							"logID":    msg.LogID,
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
						"toClient": fromClient.Username,
						"logID":    msg.LogID,
					},
				))
			}
			continue
		case MsgQueueItemType.MMS:
			if toClient != nil {
				err := router.gateway.MM4Server.sendMM4(msg)
				if err != nil {
					// throw error?
					lm.SendLog(lm.BuildLog(
						"Router.Client.MMS",
						"RouterSendMM4",
						logrus.ErrorLevel,
						map[string]interface{}{
							"toClient": toClient.Username,
							"logID":    msg.LogID,
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
						err = router.gateway.AMPQClient.Publish("toClient", marshal)
						continue
					}
					// todo maybe to add to queue via postgres?
					continue
				}

				var internal = fromClient != nil && toClient != nil
				if fromClient != nil {
					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: msg,
						Carrier:      "from_client",
						ClientID:     fromClient.ID,
						Internal:     internal,
					}
				}
				if toClient != nil {
					router.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: msg,
						Carrier:      "to_client",
						ClientID:     toClient.ID,
						Internal:     internal,
					}
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
						"toClient": toClient.Username,
						"logID":    msg.LogID,
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
				err = router.gateway.AMPQClient.Publish("toClient", marshal)
				continue
			}
		}
	}
}
