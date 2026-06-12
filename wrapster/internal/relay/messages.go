package relay

import (
	"encoding/json"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
)

func MessageType(message []byte) (string, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("nostr message must be a non-empty array")
	}
	var typ string
	if err := json.Unmarshal(raw[0], &typ); err != nil {
		return "", err
	}
	return typ, nil
}

func EventIDFromMessage(message []byte) string {
	var raw []json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil || len(raw) < 2 {
		return ""
	}
	var event struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw[1], &event); err != nil {
		return ""
	}
	return event.ID
}

func PubkeyFromEventMessage(message []byte) (string, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil {
		return "", err
	}
	if len(raw) < 2 {
		return "", fmt.Errorf("EVENT message is missing event payload")
	}
	var event struct {
		Pubkey string `json:"pubkey"`
	}
	if err := json.Unmarshal(raw[1], &event); err != nil {
		return "", err
	}
	return event.Pubkey, nil
}

func ParseEventMessage(message []byte) (nostr.Event, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil {
		return nostr.Event{}, err
	}
	if len(raw) < 2 {
		return nostr.Event{}, fmt.Errorf("EVENT message is missing event payload")
	}
	var event nostr.Event
	if err := json.Unmarshal(raw[1], &event); err != nil {
		return nostr.Event{}, err
	}
	return event, nil
}

func ParseREQMessage(message []byte) (string, []json.RawMessage, error) {
	var raw []json.RawMessage
	if err := json.Unmarshal(message, &raw); err != nil {
		return "", nil, err
	}
	if len(raw) < 3 {
		return "", nil, fmt.Errorf("REQ message is missing filter payload")
	}
	var typ string
	if err := json.Unmarshal(raw[0], &typ); err != nil {
		return "", nil, err
	}
	if typ != "REQ" {
		return "", nil, fmt.Errorf("message is not REQ")
	}
	var subID string
	if err := json.Unmarshal(raw[1], &subID); err != nil {
		return "", nil, err
	}
	filters := make([]json.RawMessage, 0, len(raw)-2)
	for _, filter := range raw[2:] {
		copied := append(json.RawMessage(nil), filter...)
		filters = append(filters, copied)
	}
	return subID, filters, nil
}

func ValidateUnauthenticatedKind0(event nostr.Event) error {
	if event.Kind != 0 {
		return fmt.Errorf("only kind 0 is allowed before auth")
	}
	if !nostr.IsValidPublicKeyHex(event.PubKey) {
		return fmt.Errorf("invalid pubkey")
	}
	if event.GetID() != event.ID {
		return fmt.Errorf("event id does not match event payload")
	}
	ok, err := event.CheckSignature()
	if err != nil || !ok {
		return fmt.Errorf("invalid event signature")
	}
	return nil
}
