package blinkogram

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/pkg/errors"
	"github.com/wolfsilver/blinko-telegram/store"
)

type Service struct {
	bot    *bot.Bot
	client *BlinkoClient
	config *Config
	store  *store.Store
	cache  *Cache

	mutex sync.Mutex
}

func NewService() (*Service, error) {
	config, err := getConfigFromEnv()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get config from env")
	}

	client := NewBlinkoClient(config.ServerAddr)

	store := store.NewStore(config.Data)
	if err := store.Init(); err != nil {
		return nil, errors.Wrap(err, "failed to init store")
	}
	s := &Service{
		config: config,
		client: client,
		store:  store,
		cache:  NewCache(),
	}
	s.cache.startGC()

	opts := []bot.Option{
		bot.WithDefaultHandler(s.handler),
		bot.WithCallbackQueryDataHandler("", bot.MatchTypePrefix, s.callbackQueryHandler),
	}
	if config.BotProxyAddr != "" {
		opts = append(opts, bot.WithServerURL(config.BotProxyAddr))
	}

	b, err := bot.New(config.BotToken, opts...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create bot")
	}
	s.bot = b

	return s, nil
}

func (s *Service) Start(ctx context.Context) {
	slog.Info("Blinkogram started")

	// set bot commands
	commands := []models.BotCommand{
		{
			Command:     "start",
			Description: "Start the bot with access token",
		},
		{
			Command:     "search",
			Description: "Search for the memos",
		},
	}
	var err error
	_, err = s.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commands})
	if err != nil {
		slog.Error("failed to set bot commands", slog.Any("err", err))
	}

	s.bot.Start(ctx)
}

func (s *Service) createMemo(content string) (BlinkoItem, error) {
	item := BlinkoItem{
		Content: content,
	}
	memo, err := s.client.UpsertBlinko(item)
	if err != nil {
		slog.Error("failed to create memo", slog.Any("err", err))
		return BlinkoItem{}, err
	}
	return memo, nil
}

func (s *Service) handleMemoCreation(m *models.Update, content string) (BlinkoItem, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var memo BlinkoItem
	var err error

	if m.Message.MediaGroupID != "" {

		// Try to get from cache first
		if cacheMemo, ok := s.cache.get(m.Message.MediaGroupID); ok {
			return cacheMemo.(BlinkoItem), nil
		}

		// Create new memo if not in cache
		memo, err = s.createMemo(content)
		if err != nil {
			return BlinkoItem{}, errors.Wrap(err, "failed to create memo for media group")
		}

		// Cache the memo with media group ID
		s.cache.set(m.Message.MediaGroupID, memo, 24*time.Hour)
	} else {
		// Handle single message
		memo, err = s.createMemo(content)
		if err != nil {
			return BlinkoItem{}, errors.Wrap(err, "failed to create memo for single message")
		}
	}

	return memo, nil
}

func (s *Service) handler(ctx context.Context, b *bot.Bot, m *models.Update) {
	if m.Message == nil {
		slog.Error("memo message is nil")
		return
	}
	message := m.Message
	if strings.HasPrefix(message.Text, "/start ") {
		s.startHandler(ctx, b, m)
		return
	} else if strings.HasPrefix(message.Text, "/search ") {
		s.searchHandler(ctx, b, m)
		return
	}

	userID := message.From.ID
	if _, ok := s.store.GetUserAccessToken(userID); !ok {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: message.Chat.ID,
			Text:   "Please start the bot with /start <access_token>",
		})
		return
	}

	content := message.Text
	contentEntities := message.Entities
	if message.Caption != "" {
		content = message.Caption
		contentEntities = message.CaptionEntities
	}
	if len(contentEntities) > 0 {
		content = formatContent(content, contentEntities)
	}

	// Add "forwarded from: originName" if message was forwarded
	if message.ForwardOrigin != nil {
		var originName, originUsername string
		// Determine the type of origin
		switch origin := message.ForwardOrigin; {
		case origin.MessageOriginUser != nil: // User
			user := origin.MessageOriginUser.SenderUser
			if user.LastName != "" {
				originName = fmt.Sprintf("%s %s", user.FirstName, user.LastName)
			} else {
				originName = user.FirstName
			}
			originUsername = user.Username
		case origin.MessageOriginHiddenUser != nil: // Hidden User
			hiddenUserName := origin.MessageOriginHiddenUser.SenderUserName
			if hiddenUserName != "" {
				originName = hiddenUserName
			} else {
				originName = "Hidden User"
			}
		case origin.MessageOriginChat != nil: // Chat
			chat := origin.MessageOriginChat.SenderChat
			originName = chat.Title
			originUsername = chat.Username
		case origin.MessageOriginChannel != nil: // Channel
			channel := origin.MessageOriginChannel.Chat
			originName = channel.Title
			originUsername = channel.Username
		}

		if originUsername != "" {
			content = fmt.Sprintf("Forwarded from [%s](https://t.me/%s)\n%s", originName, originUsername, content)
		} else {
			content = fmt.Sprintf("Forwarded from %s\n%s", originName, content)
		}
	}

	hasResource := message.Document != nil || len(message.Photo) > 0 || message.Voice != nil || message.Video != nil
	if content == "" && !hasResource {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: message.Chat.ID,
			Text:   "Please input memo content",
		})
		return
	}

	accessToken, _ := s.store.GetUserAccessToken(userID)
	s.client.UpdateToken(accessToken)

	var memo BlinkoItem
	memo, err := s.handleMemoCreation(m, content)
	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: message.Chat.ID,
			Text:   "Failed to create memo",
		})
		return
	}

	if message.Document != nil {
		s.processFileMessage(ctx, b, m, message.Document.FileID, memo)
	}
	if message.Voice != nil {
		s.processFileMessage(ctx, b, m, message.Voice.FileID, memo)
	}
	if message.Video != nil {
		s.processFileMessage(ctx, b, m, message.Video.FileID, memo)
	}
	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		s.processFileMessage(ctx, b, m, photo.FileID, memo)
	}

	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:              message.Chat.ID,
		Text:                fmt.Sprintf("Content saved as Private with %d", memo.ID),
		ParseMode:           models.ParseModeMarkdown,
		DisableNotification: true,
		ReplyParameters: &models.ReplyParameters{
			MessageID: message.ID,
		},
		ReplyMarkup: s.keyboard(memo.ID),
	})
}

func (s *Service) startHandler(ctx context.Context, b *bot.Bot, m *models.Update) {
	userID := m.Message.From.ID
	accessToken := strings.TrimPrefix(m.Message.Text, "/start ")

	s.client.UpdateToken(accessToken)
	userInfo, err := s.client.GetUserDetail()

	if err != nil {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: m.Message.Chat.ID,
			Text:   "Invalid access token",
		})
		return
	}

	s.store.SetUserAccessToken(userID, accessToken)
	b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: m.Message.Chat.ID,
		Text:   fmt.Sprintf("Hello %s!", userInfo.Nickname),
	})
}

func (s *Service) keyboard(memoId int) *models.InlineKeyboardMarkup {
	// add inline keyboard to edit memo's visibility or pinned status.
	return &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{
					Text:         "Public",
					CallbackData: fmt.Sprintf("public %d", memoId),
				},
				{
					Text:         "Private",
					CallbackData: fmt.Sprintf("private %d", memoId),
				},
				{
					Text:         "Pin",
					CallbackData: fmt.Sprintf("pin %d", memoId),
				},
			},
		},
	}
}

func (s *Service) callbackQueryHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	callbackData := update.CallbackQuery.Data
	userID := update.CallbackQuery.From.ID
	accessToken, ok := s.store.GetUserAccessToken(userID)
	if !ok {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Please start the bot with /start <access_token>",
			ShowAlert:       true,
		})
		return
	}
	s.client.UpdateToken(accessToken)

	parts := strings.Split(callbackData, " ")
	if len(parts) != 2 {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Invalid command",
			ShowAlert:       true,
		})
		return
	}
	slog.Info("parts", slog.Any("parts", parts))
	action, memoName := parts[0], parts[1]
	memoId, err := strconv.Atoi(memoName)
	if err != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Invalid memo ID",
			ShowAlert:       true,
		})
		return
	}

	memo, err := s.client.GetNoteDetail(memoId)
	if err != nil {
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            fmt.Sprintf("Memo %s not found", memoName),
			ShowAlert:       true,
		})
		return
	}

	switch action {
	case "public":
		s.shareNote(ctx, memo.ID, true, b, update)
		return
	case "private":
		s.shareNote(ctx, memo.ID, false, b, update)
		return
	case "pin":
		memo.IsTop = !memo.IsTop
	default:
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Unknown action",
			ShowAlert:       true,
		})
		return
	}

	_, e := s.client.UpsertBlinko(BlinkoItem{
		ID:      memo.ID,
		Content: memo.Content,
		IsTop:   memo.IsTop,
	})
	if e != nil {
		slog.Error("failed to update memo", slog.Any("err", e))
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Failed to update memo",
			ShowAlert:       true,
		})
		return
	}
	var pinnedMarker string
	if memo.IsTop {
		pinnedMarker = "📌"
	} else {
		pinnedMarker = ""
	}
	status := "Public"
	if !memo.IsShare {
		status = "Private"
	}
	b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      update.CallbackQuery.Message.Message.Chat.ID,
		MessageID:   update.CallbackQuery.Message.Message.ID,
		Text:        fmt.Sprintf("Memo updated as %s with %d %s", status, memo.ID, pinnedMarker),
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: s.keyboard(memo.ID),
	})

	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Memo updated",
	})
}

func (s *Service) shareNote(ctx context.Context, memoId int, share bool, b *bot.Bot, update *models.Update) bool {
	e := s.client.ShareNote(memoId, share)
	if e != nil {
		slog.Error("failed to update memo", slog.Any("err", e))
		b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: update.CallbackQuery.ID,
			Text:            "Failed to update memo",
			ShowAlert:       true,
		})
		return true
	}
	status := "Public"
	if !share {
		status = "Private"
	}
	b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      update.CallbackQuery.Message.Message.Chat.ID,
		MessageID:   update.CallbackQuery.Message.Message.ID,
		Text:        fmt.Sprintf("Memo updated as %s with %d", status, memoId),
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: s.keyboard(memoId),
	})
	b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: update.CallbackQuery.ID,
		Text:            "Memo updated",
	})
	return false
}

func (s *Service) searchHandler(ctx context.Context, b *bot.Bot, m *models.Update) {
	userID := m.Message.From.ID
	searchString := strings.TrimPrefix(m.Message.Text, "/search ")

	accessToken, _ := s.store.GetUserAccessToken(userID)
	s.client.UpdateToken(accessToken)

	results, err := s.client.GetNoteList(searchString)

	if err != nil {
		slog.Error("failed to search memos", slog.Any("err", err))
		return
	}

	if len(results) == 0 {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: m.Message.Chat.ID,
			Text:   "No memos found for the specified search criteria.",
		})
	} else {
		for _, memo := range results {
			tgMessage := fmt.Sprintf("[%d] %s", memo.ID, memo.Content)
			b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: m.Message.Chat.ID,
				Text:   tgMessage,
			})
		}
	}
}

func (s *Service) processFileMessage(ctx context.Context, b *bot.Bot, m *models.Update, fileID string, memo BlinkoItem) {
	file, err := b.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	if err != nil {
		s.sendError(b, m.Message.Chat.ID, errors.Wrap(err, "failed to get file"))
		return
	}

	_, err = s.saveResourceFromFile(file, memo)
	if err != nil {
		s.sendError(b, m.Message.Chat.ID, errors.Wrap(err, "failed to save resource"))
		return
	}
}

func (s *Service) saveResourceFromFile(file *models.File, memo BlinkoItem) (FileInfo, error) {
	fileLink := s.bot.FileDownloadLink(file)
	response, err := http.Get(fileLink)
	if err != nil {
		return FileInfo{}, errors.Wrap(err, "failed to download file")
	}
	defer response.Body.Close()

	bytes, err := io.ReadAll(response.Body)
	if err != nil {
		return FileInfo{}, errors.Wrap(err, "failed to read file")
	}

	resource, err := s.client.UploadFile(bytes, filepath.Base(file.FilePath))

	if err != nil {
		return FileInfo{}, errors.Wrap(err, "failed to create resource")
	}

	s.client.UpsertBlinko(BlinkoItem{
		ID:          memo.ID,
		Content:     memo.Content,
		Attachments: []FileInfo{resource},
	})

	return resource, nil
}

func (s *Service) sendError(b *bot.Bot, chatID int64, err error) {
	slog.Error("error", slog.Any("err", err))
	b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: chatID,
		Text:   fmt.Sprintf("Error: %s", err.Error()),
	})
}

func formatContent(content string, contentEntities []models.MessageEntity) string {
	contentRunes := utf16.Encode([]rune(content))

	var sb strings.Builder
	var prevEntity = models.MessageEntity{}
	var entityContent string
	re := regexp.MustCompile(`^(\s*)(.*)(\s*)$`)

	for _, entity := range contentEntities {
		switch entity.Type {
		case models.MessageEntityTypeURL:
		case models.MessageEntityTypeTextLink:
		case models.MessageEntityTypeBold:
		case models.MessageEntityTypeItalic:
		default:
			continue
		}

		if entity.Offset >= prevEntity.Offset+prevEntity.Length {
			sb.WriteString(entityContent)
			sb.WriteString(string(utf16.Decode(contentRunes[prevEntity.Offset+prevEntity.Length : entity.Offset])))
			entityContent = string(utf16.Decode(contentRunes[entity.Offset : entity.Offset+entity.Length]))
			prevEntity = entity
			if strings.TrimSpace(entityContent) == "" {
				continue
			}
		}

		matches := re.FindStringSubmatch(entityContent)
		switch entity.Type {
		case models.MessageEntityTypeURL:
			entityContent = fmt.Sprintf("%s[%s](%s)%s", matches[1], matches[2], matches[2], matches[3])
		case models.MessageEntityTypeTextLink:
			entityContent = fmt.Sprintf("%s[%s](%s)%s", matches[1], matches[2], entity.URL, matches[3])
		case models.MessageEntityTypeBold:
			entityContent = fmt.Sprintf("%s**%s**%s", matches[1], matches[2], matches[3])
		case models.MessageEntityTypeItalic:
			entityContent = fmt.Sprintf("%s*%s*%s", matches[1], matches[2], matches[3])
		}
	}
	sb.WriteString(entityContent)
	sb.WriteString(string(utf16.Decode(contentRunes[prevEntity.Offset+prevEntity.Length:])))
	return sb.String()
}
