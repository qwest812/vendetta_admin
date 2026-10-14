package supremacy

// Карточки героев: с какого дня партии героя можно ставить на карту,
// какая мораль нужна провинции и сколько он мобилизуется. Игровой сервер
// этого не присылает — клиент берёт это с сайта, из справочника предметов
// (getContentItems), в описании предмета героя:
//
//	"extraSettings": "{\"inventorySystemItem\":{\"itemType\":\"unit\",
//	  \"unitCardContent\":{\"mobilizationTime\":7200000,
//	  \"deploymentMoraleThreshold\":70,\"dayOfAvailability\":2, ...}}}"
//
// extraSettings — JSON строкой внутри JSON. Справочник общий для всех
// партий и меняется с обновлениями игры, поэтому держим его в памяти.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// heroCardsTTL — сколько держим справочник. Ответ весит треть мегабайта,
// а меняется он с обновлениями игры — раз в недели.
const heroCardsTTL = 12 * time.Hour

// HeroCard — то, что игра знает о герое как о предмете.
type HeroCard struct {
	// Day — с какого дня партии героя можно разместить.
	Day int
	// Morale — какая мораль нужна провинции, в процентах.
	Morale int
	// Mobilization — сколько герой собирается после размещения, игровым
	// временем: в ускоренной партии настоящего уходит меньше.
	Mobilization time.Duration
}

// HeroCards — карточки героев по номеру предмета героя.
func (c *Client) HeroCards(ctx context.Context) (map[int]HeroCard, error) {
	c.mu.Lock()
	cards, at := c.heroCards, c.heroCardsAt
	c.mu.Unlock()
	if cards != nil && time.Since(at) < heroCardsTTL {
		return cards, nil
	}

	raw, err := c.call(ctx, "getContentItems", []param{{"locale", "en"}, {"premiums", "1"}})
	if err != nil {
		return nil, fmt.Errorf("справочник предметов: %w", err)
	}
	cards, err = parseHeroCards(raw)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.heroCards, c.heroCardsAt = cards, time.Now()
	c.mu.Unlock()
	return cards, nil
}

// parseHeroCards разбирает справочник предметов. В ответе вперемешку
// всё подряд — моды, предметы, наборы, — и нам нужны только предметы
// с карточкой юнита. Номер предмета приходит строкой.
func parseHeroCards(raw json.RawMessage) (map[int]HeroCard, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("справочник предметов: разбор: %w", err)
	}
	cards := map[int]HeroCard{}
	for _, item := range items {
		var it struct {
			ID    json.RawMessage `json:"itemID"`
			Extra any             `json:"extraSettings"`
		}
		if json.Unmarshal(item, &it) != nil {
			continue
		}
		extra, ok := it.Extra.(string)
		if !ok || extra == "" {
			continue
		}
		var settings struct {
			Item struct {
				Card *struct {
					Day          int   `json:"dayOfAvailability"`
					Morale       int   `json:"deploymentMoraleThreshold"`
					Mobilization int64 `json:"mobilizationTime"`
				} `json:"unitCardContent"`
			} `json:"inventorySystemItem"`
		}
		if json.Unmarshal([]byte(extra), &settings) != nil || settings.Item.Card == nil {
			continue
		}
		id, err := strconv.Atoi(trimQuotes(string(it.ID)))
		if err != nil {
			continue
		}
		card := settings.Item.Card
		cards[id] = HeroCard{Day: card.Day, Morale: card.Morale,
			Mobilization: time.Duration(card.Mobilization) * time.Millisecond}
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("справочник предметов: ни одной карточки юнита")
	}
	return cards, nil
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
