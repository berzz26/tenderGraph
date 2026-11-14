package srv

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

type tejOHLCVResponse struct {
	Data []struct {
		Date     string  `json:"date"`
		Open     float64 `json:"open"`
		High     float64 `json:"high"`
		Low      float64 `json:"low"`
		Close    float64 `json:"close"`
		Volume   int64   `json:"volume"`
		Turnover float64 `json:"turnover"`
	} `json:"data"`
	Meta map[string]any `json:"meta"`
}

// MarketHistory proxies live TejHQ EOD data. It intentionally does not read
// or write market history in PostgreSQL; Vyuha only resolves the NSE symbol.
func (s *Server) MarketHistory(w http.ResponseWriter, r *http.Request) {
	entityID := strings.TrimSpace(r.PathValue("entityId"))
	if len(entityID) != 36 {
		fail(w, http.StatusBadRequest, "invalid entity id")
		return
	}
	entity, err := s.QueryOne(r.Context(), `SELECT ticker_nse, ticker_bse FROM entities WHERE id=$1 AND is_listed=TRUE`, entityID)
	if err != nil {
		sendErr(w, err)
		return
	}
	if entity == nil || strings.TrimSpace(fmt.Sprint(entity["ticker_nse"])) == "" {
		JSON(w, http.StatusOK, map[string]any{"data": []any{}, "benchmark": []any{}, "meta": map[string]any{"available": false, "reason": "No NSE ticker is mapped for this listed entity."}})
		return
	}

	symbol := strings.ToUpper(strings.TrimSpace(fmt.Sprint(entity["ticker_nse"])))
	from := qp(r, "from", "")
	to := qp(r, "to", "")
	endpoint := "https://api.tejhq.dev/v1/ohlcv/nse/" + symbol
	if key := strings.TrimSpace(getEnv("TEJHQ_API_KEY")); key != "" && strings.EqualFold(qp(r, "adjusted", "true"), "true") {
		endpoint = "https://api.tejhq.dev/v1/adjusted/nse/" + symbol
	}
	sep := "?"
	if from != "" {
		endpoint += sep + "from=" + from
		sep = "&"
	}
	if to != "" {
		endpoint += sep + "to=" + to
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		sendErr(w, err)
		return
	}
	if key := strings.TrimSpace(getEnv("TEJHQ_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		JSON(w, http.StatusBadGateway, map[string]any{"error": "market data provider unavailable"})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		JSON(w, http.StatusBadGateway, map[string]any{"error": "market data provider returned an error", "provider_status": resp.StatusCode})
		return
	}
	var payload tejOHLCVResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		sendErr(w, err)
		return
	}
	data := make([]map[string]any, 0, len(payload.Data))
	for _, row := range payload.Data {
		data = append(data, map[string]any{"trading_date": row.Date, "open": row.Open, "high": row.High, "low": row.Low, "close": row.Close, "adjusted_close": row.Close, "volume": row.Volume, "turnover": row.Turnover, "source": "tejhq-live", "source_symbol": symbol})
	}
	if payload.Meta == nil {
		payload.Meta = map[string]any{}
	}
	payload.Meta["available"] = true
	payload.Meta["source"] = "tejhq-live"
	payload.Meta["symbol"] = symbol
	JSON(w, http.StatusOK, map[string]any{"data": data, "benchmark": []any{}, "meta": payload.Meta})
}

func getEnv(key string) string {
	return os.Getenv(key)
}
