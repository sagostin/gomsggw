package main

import (
	"github.com/sirupsen/logrus"
)

// CarrierRouter inbound from carriers, or in the "from_carrier" channel.
// the inbound function on the carrier processors, simply pushes the msg to the rabbitmq, and
// then this thing picks it up, for outbound, it's just the reverse.
func (router *Router) CarrierRouter() {
	for {
		var lm = router.gateway.LogManager

		msg := <-router.CarrierMsgChan
		go func(r *Router, manager *LogManager, m *MsgQueueItem) {
			to, _ := FormatToE164(m.To)
			m.To = to
			from, _ := FormatToE164(m.From)
			m.From = from

			switch msgType := m.Type; msgType {
			case MsgQueueItemType.SMS:
				client, _ := r.findClientByNumber(m.To)
				if client != nil {
					session, err := r.gateway.SMPPServer.findSmppSession(m.To)
					if err != nil {
						/*if m.Delivery != nil{
							manager.SendLog(manager.BuildLog(
								"r.Carrier.SMS",
								"RouterFindSMPP",
								logrus.ErrorLevel,
								map[string]interface{}{/
									"client": client.Username,
									"logID":  m.LogID,
								}, err,
							))

							err := m.Delivery.Reject(true)
							if err != nil {
								break
							}
						} else {
							marshal, err := json.Marshal(msg)
							if err != nil {
								// todo
								break
							}
							err = r.gateway.AMPQClient.Publish("client", marshal)
							break
						}*/
						manager.SendLog(manager.BuildLog(
							"r.Carrier.SMS",
							"RouterFindSMPP",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": client.Username,
								"logID":  m.LogID,
							}, err,
						))
						msg.Retry("failed to find smpp", r.CarrierMsgChan)
						// todo maybe to add to queue via postgres?
						break
					}
					if session != nil {
						err := r.gateway.SMPPServer.sendSMPP(msg, session)
						if err != nil {
							/*if m.Delivery != nil {
								err = m.Delivery.Ack(false)
							}
							manager.SendLog(manager.BuildLog(
								"r.Carrier.SMS",
								"RouterSendSMPP",
								logrus.ErrorLevel,
								map[string]interface{}{
									"client": client.Username,
									"logID":  m.LogID,
									"msg":    msg,
								}, err,
							))
							marshal, _ := json.Marshal(msg)
							_ = r.gateway.AMPQClient.Publish("client", marshal)*/
							msg.Retry("failed to send to smpp", r.ClientMsgChan)
							break
						} else {
							r.gateway.MsgRecordChan <- MsgRecord{
								MsgQueueItem: msg,
								Carrier:      "inbound",
								ClientID:     client.ID,
								Internal:     false,
							}
							/*if m.Delivery != nil {
								err := m.Delivery.Ack(false)
								if err != nil {
									break
								}
							}*/
							break
						}
					} else {
						/*if m.Delivery != nil {
							err := m.Delivery.Nack(false, true)
							if err != nil {
								break
							}
						}*/
						msg.Retry("failed to send to smpp", r.CarrierMsgChan)
						break
					}
				}

				client, _ = r.findClientByNumber(m.From)
				if client != nil {
					// todo log & error invalid sender number
				}

				carrier, _ := r.gateway.getClientCarrier(m.From)
				if carrier != "" {
					// add to outbound carrier queue
					route := r.gateway.Router.findRouteByName("carrier", carrier)
					if route != nil {
						err := route.Handler.SendSMS(&msg)
						if err != nil {
							// todo log
							/*if m.Delivery != nil {
								err = m.Delivery.Reject(true)
							}*/
							manager.SendLog(manager.BuildLog(
								"r.Carrier.SMS",
								"RouterSendCarrier",
								logrus.ErrorLevel,
								map[string]interface{}{
									"client": client.Username,
									"logID":  m.LogID,
									"msg":    msg,
								}, err,
							))
							msg.Retry("failed to send to smpp", r.CarrierMsgChan)
							break
						}
						r.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem: msg,
							Carrier:      carrier,
							ClientID:     client.ID,
							Internal:     false,
						}
						/*if m.Delivery != nil {
							err = m.Delivery.Ack(false)
						}*/
						break
					}
				} else {
					manager.SendLog(manager.BuildLog(
						"r.Carrier.MMS",
						"RouterFindCarrier",
						logrus.ErrorLevel,
						map[string]interface{}{
							"client": client.Username,
							"logID":  m.LogID,
						},
					))
				}
				manager.SendLog(manager.BuildLog(
					"r.Carrier.SMS",
					"RouterSendFailed",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  m.LogID,
					},
				))
				break
			case MsgQueueItemType.MMS:
				client, _ := r.findClientByNumber(m.To)

				if m.Files == nil {
					/*if m.Delivery != nil {
						err := m.Delivery.Reject(false)
						if err != nil {
							break
						}
					}*/

					manager.SendLog(manager.BuildLog(
						"r.Carrier.MMS",
						"NoFiles",
						logrus.ErrorLevel,
						map[string]interface{}{
							"client": client.Username,
							"logID":  m.LogID,
						},
					))
					break
				}

				if client != nil {
					err := r.gateway.MM4Server.sendMM4(msg)
					if err != nil {
						/*if m.Delivery != nil {
							err := m.Delivery.Reject(true)
							if err != nil {
								break
							}
						} else {
							marshal, err := json.Marshal(msg)
							if err != nil {
								// todo
								break
							}
							err = r.gateway.AMPQClient.Publish("client", marshal)
							break
						}*/
						manager.SendLog(manager.BuildLog(
							"r.Carrier.MMS",
							"RouterSendMM4",
							logrus.ErrorLevel,
							map[string]interface{}{
								"client": client.Username,
								"logID":  m.LogID,
								"msg":    msg,
							}, err,
						))
						msg.Retry("failed to send mm4", r.ClientMsgChan)
						// todo maybe to add to queue via postgres?
						break
					}
					r.gateway.MsgRecordChan <- MsgRecord{
						MsgQueueItem: msg,
						Carrier:      "inbound",
						ClientID:     client.ID,
						Internal:     false,
					}
					/*if m.Delivery != nil {
						err := m.Delivery.Ack(false)
						if err != nil {
							break
						}
					}*/
					break
				}

				client, _ = r.findClientByNumber(m.From)
				if client != nil {
					// todo log & error invalid sender number
				}

				carrier, _ := r.gateway.getClientCarrier(m.From)
				if carrier != "" {
					// add to outbound carrier queue
					route := r.gateway.Router.findRouteByName("carrier", carrier)
					if route != nil {
						err := route.Handler.SendMMS(&msg)
						if err != nil {
							manager.SendLog(manager.BuildLog(
								"r.Carrier.MMS",
								"RouterSendCarrier",
								logrus.ErrorLevel,
								map[string]interface{}{
									"client": client.Username,
									"logID":  m.LogID,
									"msg":    msg,
								}, err,
							))

							msg.Retry("failed to send to carrier", r.CarrierMsgChan)
							break
						}
						r.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem: msg,
							Carrier:      carrier,
							ClientID:     client.ID,
							Internal:     false,
						}
						/*if m.Delivery != nil {
							err := m.Delivery.Ack(false)
							if err != nil {
								break
							}
						}*/
						break

					}
					break
				} else {
					manager.SendLog(manager.BuildLog(
						"r.Carrier.MMS",
						"RouterFindCarrier",
						logrus.ErrorLevel,
						map[string]interface{}{
							"client": client.Username,
							"logID":  m.LogID,
						},
					))
				}
				// throw error?
				manager.SendLog(manager.BuildLog(
					"r.Carrier.MMS",
					"RouterSendFailed",
					logrus.ErrorLevel,
					map[string]interface{}{
						"client": client.Username,
						"logID":  m.LogID,
					},
				))
				break
			}
		}(router, lm, &msg)

	}
}
