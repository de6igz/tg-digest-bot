package mtproto

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/tg"
)

// ErrUnsupportedSessionFormat is returned when MTProto session data can't be recognised.
var ErrUnsupportedSessionFormat = fmt.Errorf("unsupported MTProto session format")

// NormalizeSessionBytes converts MTProto session blobs from known formats (Telethon
// string sessions or exported JSON) to the JSON format used by gotd session.Storage.
// It returns the converted blob, a flag telling whether conversion was required and
// an error when the payload can't be recognised.
func NormalizeSessionBytes(raw []byte) ([]byte, bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, fmt.Errorf("MTProto session is empty")
	}

	// gotd session JSON already.
	var gotd struct {
		Version int `json:"Version"`
	}
	if err := json.Unmarshal(trimmed, &gotd); err == nil && gotd.Version != 0 {
		clone := append([]byte(nil), trimmed...)
		return clone, false, nil
	}

	if converted, err := convertTelethonAccountJSON(trimmed); err == nil {
		return converted, true, nil
	}

	if converted, err := convertTelethonSessionJSON(trimmed); err == nil {
		return converted, true, nil
	}

	if converted, err := convertTelethonString(trimmed); err == nil {
		return converted, true, nil
	}

	return nil, false, ErrUnsupportedSessionFormat
}

func convertTelethonAccountJSON(raw []byte) ([]byte, error) {
	var account struct {
		ExtraParams string `json:"extra_params"`
	}
	if err := json.Unmarshal(raw, &account); err != nil {
		return nil, err
	}
	if account.ExtraParams == "" {
		return nil, fmt.Errorf("telethon account JSON lacks extra_params")
	}
	return convertTelethonString([]byte(account.ExtraParams))
}

func convertTelethonSessionJSON(raw []byte) ([]byte, error) {
	type telethonRow struct {
		DCID          int    `json:"dc_id"`
		ServerAddress string `json:"server_address"`
		Port          int    `json:"port"`
		AuthKey       string `json:"auth_key"`
		TakeoutID     *int64 `json:"takeout_id"`
	}

	var rows []telethonRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.AuthKey == "" || row.ServerAddress == "" || row.Port == 0 {
			continue
		}
		return encodeSessionData(row.DCID, row.ServerAddress, row.Port, row.AuthKey)
	}
	return nil, fmt.Errorf("telethon session JSON has no usable rows")
}

func convertTelethonString(raw []byte) ([]byte, error) {
	candidate := strings.TrimSpace(string(raw))
	candidate = strings.Trim(candidate, "\"'\n\r\t")
	if candidate == "" {
		return nil, fmt.Errorf("telethon session string is empty")
	}

	data, err := session.TelethonSession(candidate)
	if err != nil {
		return nil, err
	}

	if data.Config.ThisDC == 0 {
		data.Config.ThisDC = data.DC
	}
	if data.Addr != "" && len(data.Config.DCOptions) == 0 {
		host, portStr, err := net.SplitHostPort(data.Addr)
		if err == nil {
			if port, convErr := strconv.Atoi(portStr); convErr == nil {
				data.Config.DCOptions = []tg.DCOption{{
					ID:        data.DC,
					IPAddress: host,
					Port:      port,
				}}
			}
		}
	}

	return marshalSessionData(*data)
}

func encodeSessionData(dcID int, host string, port int, authKeyHex string) ([]byte, error) {
	authKeyHex = strings.TrimSpace(authKeyHex)
	authKeyHex = strings.Trim(authKeyHex, "'\"")
	if authKeyHex == "" {
		return nil, fmt.Errorf("telethon session auth_key is empty")
	}

	rawKey, err := hex.DecodeString(authKeyHex)
	if err != nil {
		return nil, fmt.Errorf("decode auth_key: %w", err)
	}
	if len(rawKey) != len(crypto.Key{}) {
		return nil, fmt.Errorf("unexpected auth_key length: %d bytes", len(rawKey))
	}

	var key crypto.Key
	copy(key[:], rawKey)

	authKey := make([]byte, len(key))
	copy(authKey, key[:])

	id := key.WithID().ID
	authKeyID := make([]byte, len(id))
	copy(authKeyID, id[:])

	addr := net.JoinHostPort(host, strconv.Itoa(port))

	data := session.Data{
		Config: session.Config{
			ThisDC:    dcID,
			DCOptions: []tg.DCOption{{ID: dcID, IPAddress: host, Port: port}},
		},
		DC:        dcID,
		Addr:      addr,
		AuthKey:   authKey,
		AuthKeyID: authKeyID,
	}

	return marshalSessionData(data)
}

func marshalSessionData(data session.Data) ([]byte, error) {
	payload := struct {
		Version int          `json:"Version"`
		Data    session.Data `json:"Data"`
	}{
		Version: 1,
		Data:    data,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return buf, nil
}
