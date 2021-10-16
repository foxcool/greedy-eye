package telegram

import (
	"strconv"

	tb "gopkg.in/tucnak/telebot.v2"
)

type Client struct {
	bot *tb.Bot
}

func NewClient(token string) (*Client, error) {
	b, err := tb.NewBot(tb.Settings{
		Token: token,
	})
	if err != nil {
		return nil, err
	}
	return &Client{
		bot: b,
	}, nil
}

func (c *Client) SendMessage(destination, text string) error {
	chatID, err := strconv.ParseInt(destination, 10, 64)
	if err != nil {
		return err
	}
	_, err = c.bot.Send(&tb.Chat{ID: chatID}, text, &tb.SendOptions{ParseMode: tb.ModeMarkdownV2})

	return err
}
