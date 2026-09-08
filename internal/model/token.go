package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// AccessTokenResponse represents the response payload from the access-token endpoint.
type AccessTokenResponse struct {
	Status    string    `json:"status,omitempty"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// UnmarshalJSON supports both top-level and data-nested structures, as well as multiple
// field names (token, accessToken, access_token) and expiration formats (RFC3339 strings, Unix seconds, Unix millis, relative expires_in).
func (r *AccessTokenResponse) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("failed to unmarshal access token response: %w", err)
	}

	target := raw
	if dataRaw, ok := raw["data"]; ok {
		var dataObj map[string]json.RawMessage
		if err := json.Unmarshal(dataRaw, &dataObj); err == nil && len(dataObj) > 0 {
			target = dataObj
		}
	} else if resRaw, ok := raw["result"]; ok {
		var resObj map[string]json.RawMessage
		if err := json.Unmarshal(resRaw, &resObj); err == nil && len(resObj) > 0 {
			target = resObj
		}
	} else if respRaw, ok := raw["response"]; ok {
		var respObj map[string]json.RawMessage
		if err := json.Unmarshal(respRaw, &respObj); err == nil && len(respObj) > 0 {
			target = respObj
		}
	} else if payRaw, ok := raw["payload"]; ok {
		var payObj map[string]json.RawMessage
		if err := json.Unmarshal(payRaw, &payObj); err == nil && len(payObj) > 0 {
			target = payObj
		}
	}

	if statusRaw, ok := raw["status"]; ok {
		var statusStr string
		if err := json.Unmarshal(statusRaw, &statusStr); err == nil {
			r.Status = statusStr
		}
	}

	// Extract token
	var token string
	for _, key := range []string{"token", "accessToken", "access_token", "mapToken", "map_token", "x-bcrs-map-token", "x_bcrs_map_token", "jwt", "id_token"} {
		if valRaw, ok := target[key]; ok {
			var str string
			if err := json.Unmarshal(valRaw, &str); err == nil && strings.TrimSpace(str) != "" {
				token = strings.Trim(strings.TrimSpace(str), "\"")
				break
			}
		}
	}
	if token == "" {
		for _, key := range []string{"token", "accessToken", "access_token", "mapToken", "map_token", "x-bcrs-map-token", "x_bcrs_map_token"} {
			if valRaw, ok := raw[key]; ok {
				var str string
				if err := json.Unmarshal(valRaw, &str); err == nil && strings.TrimSpace(str) != "" {
					token = strings.Trim(strings.TrimSpace(str), "\"")
					break
				}
			}
		}
	}
	if token == "" {
		for _, key := range []string{"data", "result", "response", "payload"} {
			if valRaw, ok := raw[key]; ok {
				var str string
				if err := json.Unmarshal(valRaw, &str); err == nil && strings.TrimSpace(str) != "" {
					token = strings.Trim(strings.TrimSpace(str), "\"")
					break
				}
			}
		}
	}
	if token == "" {
		return fmt.Errorf("token not found in access token response")
	}
	r.Token = token

	// Extract expiresAt
	for _, key := range []string{"expiresAt", "expires_at", "expireAt", "expire_at", "expiration", "exp", "expiry", "expires_on", "not_after", "valid_to"} {
		valRaw, ok := target[key]
		if !ok {
			valRaw, ok = raw[key]
		}
		if !ok {
			continue
		}

		// Try parsing as string
		var str string
		if err := json.Unmarshal(valRaw, &str); err == nil && str != "" {
			str = strings.Trim(strings.TrimSpace(str), "\"")
			formats := []string{
				time.RFC3339Nano,
				time.RFC3339,
				"2006-01-02T15:04:05.000Z",
				"2006-01-02T15:04:05.999999999",
				"2006-01-02T15:04:05-0700",
				"2006-01-02T15:04:05",
				"2006-01-02 15:04:05.999999999",
				"2006-01-02 15:04:05 -0700",
				"2006-01-02 15:04:05-07:00",
				"2006-01-02 15:04:05",
				time.RFC1123Z,
				time.RFC1123,
			}
			for _, layout := range formats {
				if t, err := time.Parse(layout, str); err == nil {
					r.ExpiresAt = t
					return nil
				}
			}

			if f, err := strconv.ParseFloat(str, 64); err == nil && f > 0 {
				if f > 1e11 {
					r.ExpiresAt = time.UnixMilli(int64(f))
				} else {
					sec := int64(f)
					nsec := int64((f - float64(sec)) * 1e9)
					r.ExpiresAt = time.Unix(sec, nsec)
				}
				return nil
			}
		}

		// Try parsing as number
		var num float64
		if err := json.Unmarshal(valRaw, &num); err == nil && num > 0 {
			if num > 1e11 {
				r.ExpiresAt = time.UnixMilli(int64(num))
			} else {
				sec := int64(num)
				nsec := int64((num - float64(sec)) * 1e9)
				r.ExpiresAt = time.Unix(sec, nsec)
			}
			return nil
		}
	}

	// Extract relative expires_in (seconds from now)
	for _, key := range []string{"expires_in", "expiresIn", "ttl", "max_age", "maxAge", "expires_in_seconds", "expires_in_sec"} {
		valRaw, ok := target[key]
		if !ok {
			valRaw, ok = raw[key]
		}
		if !ok {
			continue
		}

		var num float64
		if err := json.Unmarshal(valRaw, &num); err == nil && num > 0 {
			if num >= 1e8 {
				if num > 1e11 {
					r.ExpiresAt = time.UnixMilli(int64(num))
				} else {
					sec := int64(num)
					nsec := int64((num - float64(sec)) * 1e9)
					r.ExpiresAt = time.Unix(sec, nsec)
				}
				return nil
			}
			r.ExpiresAt = time.Now().Add(time.Duration(num * float64(time.Second)))
			return nil
		}

		var str string
		if err := json.Unmarshal(valRaw, &str); err == nil && str != "" {
			trimmed := strings.Trim(strings.TrimSpace(str), "\"")
			if d, err := time.ParseDuration(trimmed); err == nil && d > 0 {
				r.ExpiresAt = time.Now().Add(d)
				return nil
			}
			if sec, err := strconv.ParseFloat(trimmed, 64); err == nil && sec > 0 {
				if sec >= 1e8 {
					if sec > 1e11 {
						r.ExpiresAt = time.UnixMilli(int64(sec))
					} else {
						s := int64(sec)
						ns := int64((sec - float64(s)) * 1e9)
						r.ExpiresAt = time.Unix(s, ns)
					}
					return nil
				}
				r.ExpiresAt = time.Now().Add(time.Duration(sec * float64(time.Second)))
				return nil
			}
		}
	}

	return nil
}
