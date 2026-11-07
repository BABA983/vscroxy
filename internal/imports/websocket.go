package imports

import (
	"encoding/binary"
)

// parseWebSocketFrames reads the RFC 6455 frame stream some exporters keep for
// a WebSocket transaction. It reassembles fragmented messages, exposes control
// frames, and stops at the first malformed or oversized frame. Compressed
// (RSV1) messages are skipped, including their continuation frames, so later
// frames still import. limit caps retained messages before the import budget
// is applied. truncated reports unread or malformed input; compressed reports
// an RSV1 message that was not inflated.
func parseWebSocketFrames(data []byte, direction string, limit int) ([]CaptureFrame, bool, bool) {
	var frames []CaptureFrame
	var pending []byte
	pendingKind := ""
	skipUntilFin := false
	truncated := false
	compressed := false
	if limit <= 0 {
		return nil, true, false
	}
	for pos := 0; pos < len(data); {
		if len(data)-pos < 2 {
			truncated = true
			break
		}
		first, second := data[pos], data[pos+1]
		pos += 2
		fin := first&0x80 != 0
		opcode := first & 0x0f
		rsv1 := first&0x40 != 0
		if rsv1 && opcode >= 0x8 {
			// permessage-deflate only applies to data frames; RSV1 on a
			// control frame is malformed.
			truncated = true
			break
		}
		if first&0x30 != 0 {
			truncated = true
			break
		}
		masked := second&0x80 != 0
		length := uint64(second & 0x7f)
		switch length {
		case 126:
			if len(data)-pos < 2 {
				truncated = true
				break
			}
			length = uint64(binary.BigEndian.Uint16(data[pos:]))
			pos += 2
		case 127:
			if len(data)-pos < 8 {
				truncated = true
				break
			}
			length = binary.BigEndian.Uint64(data[pos:])
			pos += 8
			if length>>63 != 0 {
				truncated = true
				break
			}
		}
		var mask [4]byte
		if masked {
			if len(data)-pos < 4 {
				truncated = true
				break
			}
			copy(mask[:], data[pos:pos+4])
			pos += 4
		}
		if length > maxCaptureBody || length > uint64(len(data)-pos) {
			truncated = true
			break
		}
		payload := make([]byte, int(length))
		copy(payload, data[pos:pos+int(length)])
		pos += int(length)
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}

		if opcode >= 0x8 {
			if !fin || len(payload) > 125 {
				truncated = true
				break
			}
			kind := ""
			switch opcode {
			case 0x8:
				kind = "close"
			case 0x9:
				kind = "ping"
			case 0xa:
				kind = "pong"
			default:
				truncated = true
				break
			}
			if kind == "" {
				break
			}
			if len(frames) >= limit {
				truncated = true
				break
			}
			frames = append(frames, CaptureFrame{Direction: direction, Kind: kind, Data: payload})
			continue
		}

		if skipUntilFin {
			if opcode == 0x0 {
				if fin {
					skipUntilFin = false
				}
				continue
			}
			if opcode == 0x1 || opcode == 0x2 {
				// A new message arrived before the skipped compressed one
				// ended. Keep the new one and report the incomplete skip.
				truncated = true
				skipUntilFin = false
			}
		}
		if rsv1 {
			// The DEFLATE payload is not shown as text/binary; context
			// takeover also makes partial inflation unsafe. Consume this
			// message and its continuation frames, then keep parsing.
			compressed = true
			if !fin {
				skipUntilFin = true
			}
			continue
		}

		switch opcode {
		case 0x0:
			if pendingKind == "" {
				truncated = true
				break
			}
			if len(pending)+len(payload) > maxCaptureBody {
				truncated = true
				break
			}
			pending = append(pending, payload...)
		case 0x1, 0x2:
			if pendingKind != "" {
				// A new data frame before the previous fragmented message
				// finished is malformed; keep the completed messages before it.
				truncated = true
				pending, pendingKind = nil, ""
				break
			}
			if opcode == 0x1 {
				pendingKind = "text"
			} else {
				pendingKind = "binary"
			}
			pending = payload
		default:
			truncated = true
			break
		}
		if truncated {
			break
		}
		if !fin {
			continue
		}
		if pendingKind == "" {
			continue
		}
		if len(pending) > maxCaptureBody || len(frames) >= limit {
			truncated = true
			break
		}
		frames = append(frames, CaptureFrame{Direction: direction, Kind: pendingKind, Data: pending})
		pending, pendingKind = nil, ""
	}
	if pendingKind != "" || skipUntilFin {
		truncated = true
	}
	return frames, truncated, compressed
}
