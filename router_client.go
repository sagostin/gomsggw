package main

import (
	"encoding/json"
	"fmt"
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
						err = router.gateway.AMPQClient.Publish("client", marshal)
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
							err = router.gateway.AMPQClient.Publish("client", marshal)
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
						err = router.gateway.AMPQClient.Publish("client", marshal)
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
				err = router.gateway.AMPQClient.Publish("client", marshal)
				continue
			}
		}
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
