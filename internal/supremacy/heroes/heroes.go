// Package heroes держит справочник героев Supremacy 1914: кто они, что умеют
// и во сколько осколков обходится каждый уровень.
//
// Справочник собран один раз и лежит рядом файлом — игра для него никуда
// не спрашивается. Обновляется он руками рута (кнопка в «Настройках»),
// и обновлённый снимок оседает в базе: перезаписать вшитый файл рантайм
// не может, а пересобирать образ ради нового героя незачем.
package heroes

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"time"
)

//go:embed heroes.json
//go:embed images/*.png
var files embed.FS

// Snapshot — весь справочник целиком. Хранится и отдаётся одним куском:
// героев два десятка, и разбирать их на таблицы незачем — читается всегда
// всё сразу, а пишется только целиком при обновлении.
type Snapshot struct {
	Version int `json:"version"`
	// CollectedAt — когда снимок собран. Это не «когда игра поменяла
	// героев»: такого игра не сообщает, и дата отвечает лишь на вопрос,
	// насколько давно мы смотрели.
	CollectedAt time.Time `json:"collected_at"`
	// Client — имя бандла клиента игры, из которого снимок взят. Меняется
	// с каждым выпуском игры, и по нему видно, что смотрели разные версии.
	Client string `json:"client"`
	Heroes []Hero `json:"heroes"`
}

// Hero — один герой. Имена лежат по-русски и по-английски рядом: интерфейс
// двуязычный, а переводит их сама игра, и брать их больше неоткуда.
type Hero struct {
	// Identifier — как героя зовёт клиент игры (FionaMaevePorter). По нему
	// сходятся тексты и картинка, и он не меняется, в отличие от имён.
	Identifier string `json:"identifier"`
	// UnitTypeID — номер типа юнита в партии. Это и есть главный ключ:
	// в составе армии герой виден именно им.
	UnitTypeID int `json:"unit_type_id"`
	// ShardItemID — предмет-осколок, которым платят за уровни.
	ShardItemID int `json:"shard_item_id"`
	// Class — род войск (Infantry, Bomber…), Rarity — редкость
	// (Uncommon…Legendary). Коды игры: подписи к ним — дело интерфейса.
	Class  string `json:"class"`
	Rarity string `json:"rarity"`
	// Image — имя файла портрета, он же лежит в images/.
	Image string `json:"image"`
	// NameEn/NameRu — полное имя («Фиона «Мейв» Портер»), Short — короткое
	// («Мейв»), Subtitle — подпись вроде «Специалист по вербовке».
	NameEn     string  `json:"name_en"`
	NameRu     string  `json:"name_ru"`
	ShortEn    string  `json:"short_en"`
	ShortRu    string  `json:"short_ru"`
	SubtitleEn string  `json:"subtitle_en"`
	SubtitleRu string  `json:"subtitle_ru"`
	Skills     []Skill `json:"skills"`
	Levels     []Level `json:"levels"`
}

// Skill — умение героя: то, что игра показывает в его карточке отдельной
// строкой. Числа в тексте игра подставляет из эффектов уровня, поэтому
// в описании остаются %s — так его показывает и она сама, пока уровень
// не выбран.
type Skill struct {
	// GroupID — как умение названо в данных партии (inf_deploy_amount_active),
	// Key — как оно названо в клиенте (InfantryDeployAmountActive).
	GroupID string `json:"group_id"`
	Key     string `json:"key"`
	NameEn  string `json:"name_en"`
	NameRu  string `json:"name_ru"`
	// Buff — короткая подпись эффекта («Количество размещаемой пехоты»).
	BuffEn string `json:"buff_en"`
	BuffRu string `json:"buff_ru"`
	TextEn string `json:"text_en"`
	TextRu string `json:"text_ru"`
}

// Level — ступень прокачки: сколько осколков стоит и что открывает.
type Level struct {
	Level int `json:"level"`
	// Shards — цена ступени в осколках героя (ShardItemID).
	Shards int `json:"shards"`
	// Items — предметы, которые игра выдаёт за эту ступень. По ним же
	// эффекты и привязаны к уровню.
	Items []int `json:"items,omitempty"`
	// Effects — что ступень меняет в бою. Пусто у героев, которых мы ещё
	// не видели в партии: цифры бафов приходят только с игрового сервера,
	// см. Fetch.
	Effects []Effect `json:"effects,omitempty"`
}

// Effect — один модификатор уровня. Поля пустые, если игра их не ставит:
// у каждого героя работают свои две-три строки из всего набора.
type Effect struct {
	// Set — имя набора в данных игры (fio_view_range_lvl10), GroupID —
	// умение, к которому набор относится.
	Set     string `json:"set"`
	GroupID string `json:"group_id"`
	// Доли, а не проценты: 0.25 — это +25%.
	SpeedFactor     float64 `json:"speed_factor,omitempty"`
	SpeedFlat       float64 `json:"speed_flat,omitempty"`
	AttackFactor    float64 `json:"attack_factor,omitempty"`
	DefenceFactor   float64 `json:"defence_factor,omitempty"`
	HitpointsFactor float64 `json:"hitpoints_factor,omitempty"`
	VisionFactor    float64 `json:"vision_factor,omitempty"`
	VisionFlat      float64 `json:"vision_flat,omitempty"`
	// Deploy — призыв войск (навык Мейв и родня): что, сколько и почём.
	Deploy *Deploy `json:"deploy,omitempty"`
	// Territory — где модификатор работает: коды территорий игры,
	// пусто — везде.
	Territory []int `json:"territory,omitempty"`
}

// Deploy — призыв войск активным навыком.
type Deploy struct {
	UnitType int `json:"unit_type"`
	Amount   int `json:"amount"`
	// TimeSec — сколько герой стоит неподвижно, в игровых секундах.
	TimeSec int     `json:"time_sec"`
	Range   float64 `json:"range,omitempty"`
	// Cost — ресурсы игры по их номерам: 1 — рыба, 20 — деньги.
	Cost map[string]int `json:"cost,omitempty"`
}

// ByUnitType — герой по типу юнита. Именно так он опознаётся в составе
// армии, и это единственный способ сказать «в этой армии Мейв».
func (s *Snapshot) ByUnitType(unitTypeID int) (Hero, bool) {
	for _, h := range s.Heroes {
		if h.UnitTypeID == unitTypeID {
			return h, true
		}
	}
	return Hero{}, false
}

// Builtin — снимок, вшитый в бинарник. Он же первичное наполнение базы
// и запасной вариант, когда в базе пусто.
func Builtin() (*Snapshot, error) {
	raw, err := files.ReadFile("heroes.json")
	if err != nil {
		return nil, fmt.Errorf("вшитый справочник героев: %w", err)
	}
	return Parse(raw)
}

// Parse разбирает снимок. Отдельной функцией затем, что читаем мы его
// из двух мест — из файла и из базы.
func Parse(raw []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("разбор справочника героев: %w", err)
	}
	return &s, nil
}

// BuiltinImage — вшитый портрет героя. Второе значение false означает,
// что такого файла нет: герой появился в игре после сборки образа,
// и портрет к нему приедет с обновлением справочника.
func BuiltinImage(name string) ([]byte, bool) {
	// Имя приходит из адреса, поэтому от пути оставляем только файл:
	// подниматься по каталогам через него нельзя.
	raw, err := files.ReadFile("images/" + path.Base(name))
	if err != nil {
		return nil, false
	}
	return raw, true
}
