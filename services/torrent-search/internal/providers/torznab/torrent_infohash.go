package torznab

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
)

// ExtractInfoHashFromTorrent computes the BitTorrent infohash (SHA1 of the bencoded "info" dict).
// It returns a lowercase hex string.
func ExtractInfoHashFromTorrent(payload []byte) (string, error) {
	start, end, ok, err := findTopLevelInfoValue(payload)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("missing info dictionary")
	}
	sum := sha1.Sum(payload[start:end])
	return hex.EncodeToString(sum[:]), nil
}

func findTopLevelInfoValue(payload []byte) (start int, end int, ok bool, err error) {
	if len(payload) == 0 || payload[0] != 'd' {
		return 0, 0, false, errors.New("invalid torrent: expected top-level dict")
	}
	i := 1
	for {
		if i >= len(payload) {
			return 0, 0, false, errors.New("invalid torrent: unexpected EOF")
		}
		if payload[i] == 'e' {
			break
		}
		key, next, parseErr := parseBencodeString(payload, i)
		if parseErr != nil {
			return 0, 0, false, parseErr
		}
		i = next
		valueStart := i
		valueEnd, skipErr := skipBencodeValue(payload, i)
		if skipErr != nil {
			return 0, 0, false, skipErr
		}
		if !ok && string(key) == "info" {
			start = valueStart
			end = valueEnd
			ok = true
		}
		i = valueEnd
	}
	return start, end, ok, nil
}

func parseBencodeString(payload []byte, i int) ([]byte, int, error) {
	if i >= len(payload) {
		return nil, 0, errors.New("invalid bencode: unexpected EOF")
	}
	n := 0
	j := i
	for {
		if j >= len(payload) {
			return nil, 0, errors.New("invalid bencode: unexpected EOF")
		}
		b := payload[j]
		if b == ':' {
			break
		}
		if b < '0' || b > '9' {
			return nil, 0, errors.New("invalid bencode: expected string length")
		}
		n = n*10 + int(b-'0')
		j++
	}
	j++ // skip ':'
	if n < 0 || j+n > len(payload) {
		return nil, 0, errors.New("invalid bencode: string out of bounds")
	}
	return payload[j : j+n], j + n, nil
}

func skipBencodeValue(payload []byte, i int) (int, error) {
	if i >= len(payload) {
		return 0, errors.New("invalid bencode: unexpected EOF")
	}
	switch payload[i] {
	case 'i':
		// int: i<number>e
		j := i + 1
		if j >= len(payload) {
			return 0, errors.New("invalid bencode: unexpected EOF")
		}
		// optional '-'
		if payload[j] == '-' {
			j++
		}
		hasDigit := false
		for {
			if j >= len(payload) {
				return 0, errors.New("invalid bencode: unexpected EOF")
			}
			if payload[j] == 'e' {
				if !hasDigit {
					return 0, errors.New("invalid bencode: empty int")
				}
				return j + 1, nil
			}
			if payload[j] < '0' || payload[j] > '9' {
				return 0, errors.New("invalid bencode: bad int")
			}
			hasDigit = true
			j++
		}
	case 'l':
		// list: l<values>e
		j := i + 1
		for {
			if j >= len(payload) {
				return 0, errors.New("invalid bencode: unexpected EOF")
			}
			if payload[j] == 'e' {
				return j + 1, nil
			}
			next, err := skipBencodeValue(payload, j)
			if err != nil {
				return 0, err
			}
			j = next
		}
	case 'd':
		// dict: d<key><value>e (keys are strings)
		j := i + 1
		for {
			if j >= len(payload) {
				return 0, errors.New("invalid bencode: unexpected EOF")
			}
			if payload[j] == 'e' {
				return j + 1, nil
			}
			_, next, err := parseBencodeString(payload, j)
			if err != nil {
				return 0, err
			}
			j = next
			next, err = skipBencodeValue(payload, j)
			if err != nil {
				return 0, err
			}
			j = next
		}
	default:
		// string: <len>:<bytes>
		_, next, err := parseBencodeString(payload, i)
		return next, err
	}
}

