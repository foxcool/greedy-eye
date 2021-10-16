package control_panel

import "errors"

// control panel - is a communication service.

type Messenger interface {
	SendMessage(destination, text string) error
}

// Service used for sending messages to user and getting commands.
type Service struct {
	// channel of information to be sent to the user
	sendChan chan interface{}
	// the channel to which the service sends errors
	errorChan chan interface{}
	// adapter for interaction with messengers or other means of communication
	bot Messenger
	// messenger chat
	chat string
}

// Service constructor
func NewService(sendChan, errorChan chan interface{}, bot Messenger, chat string) (*Service, error) {
	if sendChan == nil {
		return nil, errors.New("missing sendChan")
	}
	if errorChan == nil {
		return nil, errors.New("missing errorChan")
	}
	if bot == nil {
		return nil, errors.New("missing bot")
	}
	if chat == "" {
		return nil, errors.New("missing chat")
	}

	return &Service{
		sendChan:  sendChan,
		errorChan: errorChan,
		bot:       bot,
		chat:      chat,
	}, nil
}

// Run starts service and handle events.
func (s *Service) Run() {
	for job := range s.sendChan {
		err := s.bot.SendMessage(s.chat, job.(string))
		if err != nil {
			s.errorChan <- err
		}
	}
}
