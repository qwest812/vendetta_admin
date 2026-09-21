package supremacy

// Картинки зданий и войск берутся прямо с сайта игры: клиент игры лежит
// там открыто, и его картинки отдаются без подписи и без входа. Качать
// их к себе незачем — меняются они вместе с игрой, и свежие всегда там.
//
// Имена файлов клиент собирает так же: здание — по «ap» из справочника
// (upgrades/railway_s4.png), войско — по identifier строчными буквами
// (units/car_s3.png). Уровни одного здания делят одну картинку.

import "regexp"

const imageBase = "https://www.supremacy1914.com/clients/s1914-client-mobile/s1914-client-mobile_live/images/"

// imageKey — каким может быть имя картинки. Ключ приходит и из формы,
// поэтому в адрес попадает только то, что похоже на имя файла игры.
var imageKey = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// ValidImageKey — годится ли ключ в имя картинки.
func ValidImageKey(key string) bool { return imageKey.MatchString(key) }

// UpgradeImageURL — картинка здания; пусто, если ключ негодный.
func UpgradeImageURL(key string) string {
	if !ValidImageKey(key) {
		return ""
	}
	return imageBase + "upgrades/" + key + "_s5.png"
}

// UnitImageURL — картинка войска; пусто, если ключ негодный.
func UnitImageURL(key string) string {
	if !ValidImageKey(key) {
		return ""
	}
	return imageBase + "units/" + key + "_s3.png"
}
