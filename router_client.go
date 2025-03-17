package main

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"time"
)

// ClientRouter starts the router that handles inbound and outbound from the sms and mms servers
func (router *Router) ClientRouter() {
	for {
		msg := <-router.ClientMsgChan
		var lm = router.gateway.LogManager

		go func(r *Router, manager *LogManager, m *MsgQueueItem) {
			to, _ := FormatToE164(m.To)
			m.To = to
			from, _ := FormatToE164(m.From)
			m.From = from

			toClient, _ := r.findClientByNumber(m.To)
			fromClient, _ := r.findClientByNumber(m.From)

			if fromClient == nil && toClient == nil {
				manager.SendLog(manager.BuildLog(
					"r.Client.SMS",
					"Invalid sender number.",
					logrus.ErrorLevel,
					map[string]interface{}{
						"logID": m.LogID,
						"from":  m.From,
					},
				))
				return
			}

			switch msgType := m.Type; msgType {
			case MsgQueueItemType.SMS:
				if toClient != nil {
					session, err := r.gateway.SMPPServer.findSmppSession(m.To)
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
							m.QueuedTimestamp = time.Now()
							err = r.gateway.AMPQClient.Publish("client", marshal)
							break
						}*/
						m.Retry("failed to find smpp session", r.ClientMsgChan)
						manager.SendLog(manager.BuildLog(
							"r.Client.SMS",
							"RouterFindSMPP",
							logrus.ErrorLevel,
							map[string]interface{}{
								"toClient": toClient.Username,
								"logID":    m.LogID,
							}, err,
						))
						// todo maybe to add to queue via postgres?
						break
					}
					if session != nil {
						err := r.gateway.SMPPServer.sendSMPP(msg, session)
						if err != nil {
							manager.SendLog(manager.BuildLog(
								"r.Carrier.SMS",
								"RouterSendSMPP",
								logrus.ErrorLevel,
								map[string]interface{}{
									"toClient": toClient.Username,
									"logID":    m.LogID,
									"msg":      msg,
								}, err,
							))

							/*if m.Delivery != nil {
								err := m.Delivery.Reject(true)
								if err != nil {
									break
								}
								break
							} else {
								marshal, err := json.Marshal(msg)
								if err != nil {
									// todo
									break
								}
								m.QueuedTimestamp = time.Now()
								err = r.gateway.AMPQClient.Publish("client", marshal)
								break
							}*/
							m.Retry("failed to send smpp", r.ClientMsgChan)
						} else {
							var internal = fromClient != nil && toClient != nil
							if fromClient != nil {
								r.gateway.MsgRecordChan <- MsgRecord{
									MsgQueueItem: msg,
									Carrier:      "from_client",
									ClientID:     fromClient.ID,
									Internal:     internal,
								}
							}
							if toClient != nil {
								r.gateway.MsgRecordChan <- MsgRecord{
									MsgQueueItem: msg,
									Carrier:      "to_client",
									ClientID:     toClient.ID,
									Internal:     internal,
								}
							}
							/*if m.Delivery != nil {
								err := m.Delivery.Ack(false)
								if err != nil {
									break
								}
							}*/
						}
						break
					} else {
						manager.SendLog(manager.BuildLog(
							"r.Client.SMS",
							"RouterFindSMPP",
							logrus.ErrorLevel,
							map[string]interface{}{
								"toClient": toClient.Username,
								"logID":    m.LogID,
							}, err,
						))

						m.Retry("failed to find smpp", r.ClientMsgChan)

						/*if m.Delivery != nil {
							err := m.Delivery.Nack(false, true)
							if err != nil {
								break
							}
						}*/
						break
					}
				}

				carrier, _ := r.gateway.getClientCarrier(m.From)
				if carrier != "" {
					/*marshal, err := json.Marshal(msg)
					if err != nil {
						// todo
						break
					}
					// add to outbound carrier queue
					m.QueuedTimestamp = time.Now()
					err = r.gateway.AMPQClient.Publish("carrier", marshal)
					if err != nil {
						// todo
						break
					}*/
					r.CarrierMsgChan <- *m
					break
				} else {
					manager.SendLog(manager.BuildLog(
						"r.Client.SMS",
						"RouterFindCarrier",
						logrus.ErrorLevel,
						map[string]interface{}{
							"toClient": fromClient.Username,
							"logID":    m.LogID,
						},
					))
				}
				break
			case MsgQueueItemType.MMS:
				if toClient != nil {
					err := r.gateway.MM4Server.sendMM4(msg)
					if err != nil {
						// throw error?
						manager.SendLog(manager.BuildLog(
							"r.Client.MMS",
							"RouterSendMM4",
							logrus.ErrorLevel,
							map[string]interface{}{
								"toClient": toClient.Username,
								"logID":    m.LogID,
								"msg":      msg,
							}, err,
						))

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
							m.QueuedTimestamp = time.Now()
							err = r.gateway.AMPQClient.Publish("client", marshal)
							break
						}*/
						m.Retry("failed to send mm4", r.ClientMsgChan)
						// todo maybe to add to queue via postgres?
						break
					}

					var internal = fromClient != nil && toClient != nil
					if fromClient != nil {
						r.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem: msg,
							Carrier:      "from_client",
							ClientID:     fromClient.ID,
							Internal:     internal,
						}
					}
					if toClient != nil {
						r.gateway.MsgRecordChan <- MsgRecord{
							MsgQueueItem: msg,
							Carrier:      "to_client",
							ClientID:     toClient.ID,
							Internal:     internal,
						}
					}
					/*if m.Delivery != nil {
						err := m.Delivery.Ack(false)
						if err != nil {
							break
						}
					}*/
					break
				}

				carrier, _ := r.gateway.getClientCarrier(m.From)
				if carrier != "" {
					/*marshal, err := json.Marshal(msg)
					if err != nil {
						// todo
						break
					}
					if m.Delivery != nil {
						err := m.Delivery.Ack(false)
						if err != nil {
							break
						}
					}*/
					// add to outbound carrier queue
					m.QueuedTimestamp = time.Now()
					r.CarrierMsgChan <- *m
					/*err = r.gateway.AMPQClient.Publish("carrier", marshal)
					if err != nil {
						// todo
						break
					}*/
					break
				} else {
					manager.SendLog(manager.BuildLog(
						"r.Client.MMS",
						"RouterFindCarrier",
						logrus.ErrorLevel,
						map[string]interface{}{
							"toClient": toClient.Username,
							"logID":    m.LogID,
						},
					))
				}

				m.Retry("failed sending msg", r.ClientMsgChan)

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
					m.QueuedTimestamp = time.Now()
					err = r.gateway.AMPQClient.Publish("client", marshal)
					break
				}*/
			}
		}(router, lm, &msg)
	}
}

// updateClientPassword updates both the encrypted database password and decrypted in-memory password
func (gateway *Gateway) updateClientPassword(clientID uint, newPassword string) error {
	// First encrypt the new password
	encryptedPassword, err := EncryptPassword(newPassword, gateway.EncryptionKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt new password: %w", err)
	}

	// Update the encrypted password in the database
	if err := gateway.DB.Model(&Client{}).Where("id = ?", clientID).Update("password", encryptedPassword).Error; err != nil {
		return fmt.Errorf("failed to update password in database: %w", err)
	}

	// Update the decrypted password in memory
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	// Find the client by ID in the in-memory map
	var targetClient *Client
	for _, client := range gateway.Clients {
		if client.ID == clientID {
			targetClient = client
			break
		}
	}

	if targetClient == nil {
		return fmt.Errorf("client not found in memory")
	}

	// Update the in-memory password with the decrypted version
	targetClient.Password = newPassword

	gateway.LogManager.SendLog(gateway.LogManager.BuildLog(
		"Client.UpdatePassword",
		fmt.Sprintf("Updated password for client %s", targetClient.Username),
		logrus.InfoLevel,
		map[string]interface{}{
			"client_id": clientID,
		},
	))

	return nil
}
