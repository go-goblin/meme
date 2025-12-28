package meme

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Config содержит настройки для генерации демотиватора
type Config struct {
	// Тексты
	TopText    string
	BottomText string

	// Настройки шрифта
	FontSize float64
	FontPath string // путь к файлу .ttf шрифта (опционально)
	FontData []byte // raw данные шрифта (альтернатива FontPath)

	// Настройки рамки
	Padding int
	Border  int

	// Цвета
	BackgroundColor  color.Color
	BorderColor      color.Color
	TextColor        color.Color
	TextOutlineColor color.Color
	TextOutlineWidth int

	// Настройки текста
	TextUppercase bool // Автоматически преобразовывать текст в верхний регистр
	AutoFontSize  bool // Автоматически подбирать размер шрифта под ширину изображения
}

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() *Config {
	return &Config{
		FontSize:         48,
		Padding:          80,
		Border:           10,
		BackgroundColor:  color.RGBA{0, 0, 0, 255},
		BorderColor:      color.RGBA{255, 255, 255, 255},
		TextColor:        color.RGBA{255, 255, 255, 255},
		TextOutlineColor: color.RGBA{0, 0, 0, 255},
		TextOutlineWidth: 6,
		TextUppercase:    true,
		AutoFontSize:     true,
	}
}

// Generator - генератор мемов
type Generator struct {
	config *Config
	// Кеш загруженных шрифтов: путь -> *opentype.Font
	fontCache   map[string]*opentype.Font
	fontCacheMu sync.RWMutex
}

// NewGenerator создает новый генератор с конфигурацией
func NewGenerator(config *Config) *Generator {
	if config == nil {
		config = DefaultConfig()
	}
	return &Generator{
		config:    config,
		fontCache: make(map[string]*opentype.Font),
	}
}

// Config возвращает текущую конфигурацию (можно изменять)
func (g *Generator) Config() *Config {
	return g.config
}

// Generate создает демотиватор из изображения
func (g *Generator) Generate(img image.Image) (*image.RGBA, error) {
	cfg := g.config

	// Применяем преобразование регистра если нужно
	topText := cfg.TopText
	bottomText := cfg.BottomText
	if cfg.TextUppercase {
		topText = toUpperSafe(topText)
		bottomText = toUpperSafe(bottomText)
	}

	srcBounds := img.Bounds()
	imgWidth := srcBounds.Dx()
	imgHeight := srcBounds.Dy()

	// Автоматически подбираем размер шрифта если включено
	fontSize := cfg.FontSize
	if cfg.AutoFontSize {
		// Базовый размер + корректировка под ширину
		baseSize := 48.0
		scaleFactor := float64(imgWidth) / 800.0 // 800px - базовая ширина
		if scaleFactor < 0.5 {
			scaleFactor = 0.5
		} else if scaleFactor > 2.0 {
			scaleFactor = 2.0
		}
		fontSize = baseSize * scaleFactor
	}

	// Рассчитываем размеры результата
	textHeight := 0
	if topText != "" {
		textHeight += int(fontSize * 1.5)
	}
	if bottomText != "" {
		textHeight += int(fontSize * 1.5)
	}

	resultWidth := imgWidth + cfg.Padding*2
	resultHeight := imgHeight + cfg.Padding*2 + textHeight

	// Создаем изображение для результата
	out := image.NewRGBA(image.Rect(0, 0, resultWidth, resultHeight))

	// Заливаем фон
	draw.Draw(out, out.Bounds(), &image.Uniform{cfg.BackgroundColor}, image.Point{}, draw.Src)

	// Рисуем рамку
	for i := 0; i < cfg.Border; i++ {
		rect := image.Rect(
			cfg.Padding-cfg.Border+i,
			cfg.Padding-cfg.Border+i,
			cfg.Padding+imgWidth+cfg.Border-i,
			cfg.Padding+imgHeight+cfg.Border-i,
		)
		draw.Draw(out, rect, &image.Uniform{cfg.BorderColor}, image.Point{}, draw.Src)
	}

	// Вставляем оригинальное изображение
	draw.Draw(
		out,
		image.Rect(cfg.Padding, cfg.Padding, cfg.Padding+imgWidth, cfg.Padding+imgHeight),
		img,
		srcBounds.Min,
		draw.Over,
	)

	// Загружаем шрифт с указанным размером
	fontFace, err := g.loadFont(fontSize)
	if err != nil {
		return nil, fmt.Errorf("не удалось загрузить шрифт: %w", err)
	}
	defer fontFace.Close()

	// Позиционируем текст
	currentY := cfg.Padding + imgHeight + int(fontSize*0.8)

	// Добавляем верхний текст
	if topText != "" {
		g.drawCenteredText(out, fontFace, topText, currentY)
		currentY += int(fontSize * 1.2)
	}

	// Добавляем нижний текст
	if bottomText != "" {
		g.drawCenteredText(out, fontFace, bottomText, currentY)
	}

	return out, nil
}

// loadFont загружает шрифт в зависимости от конфигурации
func (g *Generator) loadFont(size float64) (font.Face, error) {
	cfg := g.config

	var fontBytes []byte
	var cacheKey string

	// Определяем источник данных шрифта
	switch {
	case len(cfg.FontData) > 0:
		fontBytes = cfg.FontData
		cacheKey = "embedded_font_data"

	case cfg.FontPath != "":
		// Загружаем из файла с кешированием
		cacheKey = cfg.FontPath

		// Проверяем кеш
		g.fontCacheMu.RLock()
		cachedFont, ok := g.fontCache[cacheKey]
		g.fontCacheMu.RUnlock()

		if ok && cachedFont != nil {
			// Используем кешированный шрифт
			face, err := opentype.NewFace(cachedFont, &opentype.FaceOptions{
				Size:    size,
				DPI:     72,
				Hinting: font.HintingFull,
			})
			if err != nil {
				return nil, fmt.Errorf("ошибка создания face из кеша: %w", err)
			}
			return face, nil
		}

		// Загружаем из файла
		var err error
		fontBytes, err = g.loadFontFromFile(cfg.FontPath)
		if err != nil {
			return nil, err
		}

	default:
		// Используем встроенный жирный шрифт Go по умолчанию
		fontBytes = gobold.TTF
		cacheKey = "gobold_embedded"
	}

	// Парсим шрифт
	parsedFont, err := opentype.Parse(fontBytes)
	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга шрифта: %w", err)
	}

	// Кешируем если это файловый шрифт
	if cfg.FontPath != "" {
		g.fontCacheMu.Lock()
		g.fontCache[cacheKey] = parsedFont
		g.fontCacheMu.Unlock()
	}

	// Создаем face
	face, err := opentype.NewFace(parsedFont, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, fmt.Errorf("ошибка создания font face: %w", err)
	}

	return face, nil
}

// loadFontFromFile загружает шрифт из файла с валидацией
func (g *Generator) loadFontFromFile(path string) ([]byte, error) {
	// Проверяем существование файла
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("файл шрифта не найден: %s", path)
		}
		return nil, fmt.Errorf("ошибка доступа к файлу шрифта: %w", err)
	}

	// Проверяем размер файла (не должен быть слишком большим или маленьким)
	if fileInfo.Size() == 0 {
		return nil, errors.New("файл шрифта пустой")
	}
	if fileInfo.Size() > 10*1024*1024 { // 10MB максимум
		return nil, fmt.Errorf("файл шрифта слишком большой: %d байт", fileInfo.Size())
	}

	// Читаем файл
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения файла шрифта: %w", err)
	}

	// Базовая валидация что это TTF/OTF файл
	// TTF/OTF файлы начинаются с определённых сигнатур
	if len(data) < 4 {
		return nil, errors.New("файл слишком маленький для шрифта")
	}

	// Проверяем сигнатуры TTF/OTF
	// 0x00 01 00 00 - TrueType
	// 0x4F 54 54 4F - OTF/CFF (OTTO)
	// 0x74 72 75 65 - true (Type 1, устаревший)
	sig1 := string(data[:4])
	sig2 := string(data[0:4])
	if sig1 != "\x00\x01\x00\x00" && sig2 != "OTTO" && sig2 != "true" {
		// Попробуем всё равно распарсить, может быть валидным
		// Продолжаем без ошибки
	}

	return data, nil
}

// LoadFontFile загружает шрифт из файла и устанавливает его в конфигурацию
// Удобный метод для динамической загрузки шрифтов
func (g *Generator) LoadFontFile(path string) error {
	data, err := g.loadFontFromFile(path)
	if err != nil {
		return err
	}

	g.config.FontData = data
	g.config.FontPath = path // сохраняем путь для кеширования

	return nil
}

// ClearFontCache очищает кеш шрифтов
func (g *Generator) ClearFontCache() {
	g.fontCacheMu.Lock()
	g.fontCache = make(map[string]*opentype.Font)
	g.fontCacheMu.Unlock()
}

// PreloadFont предзагружает шрифт в кеш
func (g *Generator) PreloadFont(path string) error {
	data, err := g.loadFontFromFile(path)
	if err != nil {
		return err
	}

	parsedFont, err := opentype.Parse(data)
	if err != nil {
		return fmt.Errorf("ошибка парсинга предзагружаемого шрифта: %w", err)
	}

	g.fontCacheMu.Lock()
	g.fontCache[path] = parsedFont
	g.fontCacheMu.Unlock()

	return nil
}

// drawCenteredText рисует текст по центру
func (g *Generator) drawCenteredText(img *image.RGBA, face font.Face, text string, y int) {
	if text == "" {
		return
	}

	cfg := g.config

	// Создаем drawer для измерения
	d := &font.Drawer{
		Dst:  img,
		Face: face,
	}

	// Измеряем ширину текста
	textWidth := d.MeasureString(text).Ceil()
	x := (img.Bounds().Dx() - textWidth) / 2

	// Рисуем обводку если нужно
	if cfg.TextOutlineWidth > 0 {
		d.Src = image.NewUniform(cfg.TextOutlineColor)

		// Рисуем обводку по кругу
		for dx := -cfg.TextOutlineWidth; dx <= cfg.TextOutlineWidth; dx++ {
			for dy := -cfg.TextOutlineWidth; dy <= cfg.TextOutlineWidth; dy++ {
				// Пропускаем центр чтобы не перекрывать
				if dx == 0 && dy == 0 {
					continue
				}
				d.Dot = fixed.P(x+dx, y+dy)
				d.DrawString(text)
			}
		}
	}

	// Рисуем основной текст
	d.Src = image.NewUniform(cfg.TextColor)
	d.Dot = fixed.P(x, y)
	d.DrawString(text)
}

// Helper function for safe uppercase conversion
func toUpperSafe(s string) string {
	return strings.ToUpper(s)
}

// GenerateWithText - удобная функция для быстрой генерации
func GenerateWithText(img image.Image, topText, bottomText string) (*image.RGBA, error) {
	cfg := DefaultConfig()
	cfg.TopText = topText
	cfg.BottomText = bottomText

	generator := NewGenerator(cfg)
	return generator.Generate(img)
}

// GenerateWithCustomFont - генерация с кастомным шрифтом
func GenerateWithCustomFont(img image.Image, topText, bottomText, fontPath string) (*image.RGBA, error) {
	cfg := DefaultConfig()
	cfg.TopText = topText
	cfg.BottomText = bottomText
	cfg.FontPath = fontPath

	generator := NewGenerator(cfg)
	return generator.Generate(img)
}

// ValidateFontFile проверяет что файл является валидным шрифтом
func ValidateFontFile(path string) error {
	// Простая проверка существования и чтения
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("ошибка чтения файла: %w", err)
	}

	if len(data) < 4 {
		return errors.New("файл слишком мал для шрифта")
	}

	// Пытаемся распарсить чтобы убедиться что это валидный шрифт
	_, err = opentype.Parse(data)
	if err != nil {
		return fmt.Errorf("невалидный файл шрифта: %w", err)
	}

	return nil
}

// GetAvailableFonts возвращает список доступных встроенных шрифтов
func GetAvailableFonts() map[string][]byte {
	return map[string][]byte{
		"regular": goregular.TTF,
		"bold":    gobold.TTF,
	}
}
