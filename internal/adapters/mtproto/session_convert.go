package mtproto

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
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
	if looksLikeSQLite(raw) {
		converted, err := convertTelethonSessionSQLite(raw)
		if err != nil {
			return nil, false, err
		}
		return converted, true, nil
	}

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

func looksLikeSQLite(raw []byte) bool {
	const header = "SQLite format 3\x00"
	return len(raw) >= len(header) && bytes.HasPrefix(raw, []byte(header))
}

func convertTelethonSessionSQLite(raw []byte) ([]byte, error) {
	if len(raw) < 100 {
		return nil, fmt.Errorf("telethon sqlite session is too short")
	}

	pageSize := int(binary.BigEndian.Uint16(raw[16:18]))
	if pageSize == 0 {
		return nil, fmt.Errorf("telethon sqlite session has zero page size")
	}
	if pageSize == 1 {
		pageSize = 65536
	}
	if len(raw) < pageSize {
		return nil, fmt.Errorf("telethon sqlite session truncated: expected at least %d bytes", pageSize)
	}

	schemaRecords, err := readTableRecords(raw, pageSize, 1)
	if err != nil {
		return nil, fmt.Errorf("read sqlite schema: %w", err)
	}

	var rootPage int
	for _, rec := range schemaRecords {
		if len(rec) < 5 {
			continue
		}
		kind, _ := rec[0].(string)
		name, _ := rec[1].(string)
		tblName, _ := rec[2].(string)
		root, ok := toInt(rec[3])
		if kind == "table" && name == "sessions" && tblName == "sessions" && ok {
			rootPage = root
			break
		}
	}

	if rootPage == 0 {
		return nil, fmt.Errorf("telethon sqlite session lacks sessions table")
	}

	sessionRecords, err := readTableRecords(raw, pageSize, rootPage)
	if err != nil {
		return nil, fmt.Errorf("read sessions table: %w", err)
	}

	for _, rec := range sessionRecords {
		if len(rec) < 5 {
			continue
		}

		dcID, ok := toInt(rec[1])
		if !ok {
			continue
		}
		host, _ := rec[2].(string)
		if host == "" {
			continue
		}
		port, ok := toInt(rec[3])
		if !ok {
			continue
		}

		var authHex string
		switch v := rec[4].(type) {
		case []byte:
			authHex = hex.EncodeToString(v)
		case string:
			authHex = v
		default:
			continue
		}

		payload, err := encodeSessionData(dcID, host, port, authHex)
		if err != nil {
			return nil, err
		}
		return payload, nil
	}

	return nil, fmt.Errorf("telethon sqlite session has no usable rows")
}

type sqliteRecord []interface{}

func readTableRecords(raw []byte, pageSize int, pageNumber int) ([]sqliteRecord, error) {
	if pageNumber <= 0 {
		return nil, fmt.Errorf("invalid page number %d", pageNumber)
	}
	start := (pageNumber - 1) * pageSize
	if start >= len(raw) {
		return nil, fmt.Errorf("page %d out of range", pageNumber)
	}
	end := start + pageSize
	if end > len(raw) {
		end = len(raw)
	}
	page := raw[start:end]

	headerOffset := 0
	if pageNumber == 1 {
		if len(page) < 100 {
			return nil, fmt.Errorf("page %d shorter than database header", pageNumber)
		}
		headerOffset = 100
	}

	if len(page[headerOffset:]) < 8 {
		return nil, fmt.Errorf("page %d too small", pageNumber)
	}

	pageType := page[headerOffset]
	switch pageType {
	case 0x0d:
		return readTableLeaf(page, headerOffset)
	case 0x05:
		return readTableInterior(raw, pageSize, page, headerOffset)
	default:
		return nil, fmt.Errorf("unsupported b-tree page type 0x%02x", pageType)
	}
}

func readTableInterior(raw []byte, pageSize int, page []byte, offset int) ([]sqliteRecord, error) {
	if len(page[offset:]) < 12 {
		return nil, fmt.Errorf("interior page truncated")
	}

	cellCount := int(binary.BigEndian.Uint16(page[offset+3 : offset+5]))
	rightMost := int(binary.BigEndian.Uint32(page[offset+8 : offset+12]))
	ptrBase := offset + 12
	if ptrBase+cellCount*2 > len(page) {
		return nil, fmt.Errorf("interior cell pointer array truncated")
	}

	var rows []sqliteRecord
	for i := 0; i < cellCount; i++ {
		cellOffset := int(binary.BigEndian.Uint16(page[ptrBase+2*i : ptrBase+2*i+2]))
		if cellOffset+4 > len(page) {
			return nil, fmt.Errorf("interior cell %d truncated", i)
		}
		childPage := int(binary.BigEndian.Uint32(page[cellOffset : cellOffset+4]))
		childRows, err := readTableRecords(raw, pageSize, childPage)
		if err != nil {
			return nil, err
		}
		rows = append(rows, childRows...)
	}

	if rightMost != 0 {
		childRows, err := readTableRecords(raw, pageSize, rightMost)
		if err != nil {
			return nil, err
		}
		rows = append(rows, childRows...)
	}

	return rows, nil
}

func readTableLeaf(page []byte, offset int) ([]sqliteRecord, error) {
	cellCount := int(binary.BigEndian.Uint16(page[offset+3 : offset+5]))
	ptrBase := offset + 8
	if ptrBase+cellCount*2 > len(page) {
		return nil, fmt.Errorf("leaf cell pointer array truncated")
	}

	rows := make([]sqliteRecord, 0, cellCount)
	for i := 0; i < cellCount; i++ {
		cellOffset := int(binary.BigEndian.Uint16(page[ptrBase+2*i : ptrBase+2*i+2]))
		record, err := parseTableLeafCell(page[cellOffset:])
		if err != nil {
			return nil, err
		}
		rows = append(rows, record)
	}
	return rows, nil
}

func parseTableLeafCell(cell []byte) (sqliteRecord, error) {
	payloadLen, n, err := readVarint(cell)
	if err != nil {
		return nil, err
	}
	cell = cell[n:]

	_, n, err = readVarint(cell)
	if err != nil {
		return nil, err
	}
	cell = cell[n:]

	if payloadLen > uint64(len(cell)) {
		return nil, fmt.Errorf("payload truncated: need %d bytes, have %d", payloadLen, len(cell))
	}

	payload := cell[:payloadLen]
	return parseRecordPayload(payload)
}

func parseRecordPayload(payload []byte) (sqliteRecord, error) {
	headerLen64, n, err := readVarint(payload)
	if err != nil {
		return nil, err
	}
	headerLen := int(headerLen64)
	if headerLen < n || headerLen > len(payload) {
		return nil, fmt.Errorf("invalid record header length")
	}

	header := payload[:headerLen]
	body := payload[headerLen:]

	var serials []uint64
	for offset := n; offset < headerLen; {
		serial, size, err := readVarint(header[offset:])
		if err != nil {
			return nil, err
		}
		serials = append(serials, serial)
		offset += size
	}

	values := make(sqliteRecord, len(serials))
	bodyOffset := 0
	for i, serial := range serials {
		switch {
		case serial == 0:
			values[i] = nil
		case serial == 1:
			if bodyOffset+1 > len(body) {
				return nil, fmt.Errorf("int8 column truncated")
			}
			values[i] = int64(int8(body[bodyOffset]))
			bodyOffset++
		case serial == 2:
			if bodyOffset+2 > len(body) {
				return nil, fmt.Errorf("int16 column truncated")
			}
			values[i] = decodeSignedInt(body[bodyOffset : bodyOffset+2])
			bodyOffset += 2
		case serial == 3:
			if bodyOffset+3 > len(body) {
				return nil, fmt.Errorf("int24 column truncated")
			}
			values[i] = decodeSignedInt(body[bodyOffset : bodyOffset+3])
			bodyOffset += 3
		case serial == 4:
			if bodyOffset+4 > len(body) {
				return nil, fmt.Errorf("int32 column truncated")
			}
			values[i] = decodeSignedInt(body[bodyOffset : bodyOffset+4])
			bodyOffset += 4
		case serial == 5:
			if bodyOffset+6 > len(body) {
				return nil, fmt.Errorf("int48 column truncated")
			}
			values[i] = decodeSignedInt(body[bodyOffset : bodyOffset+6])
			bodyOffset += 6
		case serial == 6:
			if bodyOffset+8 > len(body) {
				return nil, fmt.Errorf("int64 column truncated")
			}
			values[i] = decodeSignedInt(body[bodyOffset : bodyOffset+8])
			bodyOffset += 8
		case serial == 7:
			if bodyOffset+8 > len(body) {
				return nil, fmt.Errorf("float64 column truncated")
			}
			bits := binary.BigEndian.Uint64(body[bodyOffset : bodyOffset+8])
			values[i] = math.Float64frombits(bits)
			bodyOffset += 8
		case serial == 8:
			values[i] = int64(0)
		case serial == 9:
			values[i] = int64(1)
		case serial >= 12 && serial%2 == 0:
			length := int((serial - 12) / 2)
			if bodyOffset+length > len(body) {
				return nil, fmt.Errorf("blob column truncated")
			}
			buf := make([]byte, length)
			copy(buf, body[bodyOffset:bodyOffset+length])
			values[i] = buf
			bodyOffset += length
		case serial >= 13 && serial%2 == 1:
			length := int((serial - 13) / 2)
			if bodyOffset+length > len(body) {
				return nil, fmt.Errorf("text column truncated")
			}
			values[i] = string(body[bodyOffset : bodyOffset+length])
			bodyOffset += length
		default:
			return nil, fmt.Errorf("unsupported serial type %d", serial)
		}
	}
	return values, nil
}

func decodeSignedInt(buf []byte) int64 {
	var value int64
	for _, b := range buf {
		value = (value << 8) | int64(b)
	}
	bitLen := uint(len(buf) * 8)
	signMask := int64(1) << (bitLen - 1)
	if value&signMask != 0 {
		value -= int64(uint64(1) << bitLen)
	}
	return value
}

func readVarint(buf []byte) (uint64, int, error) {
	var value uint64
	for i := 0; i < len(buf) && i < 8; i++ {
		value = (value << 7) | uint64(buf[i]&0x7F)
		if buf[i]&0x80 == 0 {
			return value, i + 1, nil
		}
	}
	if len(buf) < 9 {
		return 0, 0, fmt.Errorf("varint truncated")
	}
	value = (value << 8) | uint64(buf[8])
	return value, 9, nil
}

func toInt(v interface{}) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case int32:
		return int(val), true
	default:
		return 0, false
	}
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
