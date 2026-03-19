package whatsapp

// TextMessage represents a simple text message payload
type TextMessage struct {
	MessagingProduct string `json:"messaging_product"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Text             struct {
		Body string `json:"body"`
	} `json:"text"`
}

// InteractiveButtonMessage represents an interactive button message
type InteractiveButtonMessage struct {
	MessagingProduct string `json:"messaging_product"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Interactive      struct {
		Type   string `json:"type"`
		Body   struct {
			Text string `json:"text"`
		} `json:"body"`
		Action struct {
			Buttons []struct {
				Type  string `json:"type"`
				Reply struct {
					ID    string `json:"id"`
					Title string `json:"title"`
				} `json:"reply"`
			} `json:"buttons"`
		} `json:"action"`
	} `json:"interactive"`
}

// InteractiveListMessage represents an interactive list message
type InteractiveListMessage struct {
	MessagingProduct string `json:"messaging_product"`
	To               string `json:"to"`
	Type             string `json:"type"`
	Interactive      struct {
		Type   string `json:"type"`
		Body   struct {
			Text string `json:"text"`
		} `json:"body"`
		Action struct {
			Button   string `json:"button"`
			Sections []struct {
				Title string `json:"title,omitempty"`
				Rows  []struct {
					ID          string `json:"id"`
					Title       string `json:"title"`
					Description string `json:"description,omitempty"`
				} `json:"rows"`
			} `json:"sections"`
		} `json:"action"`
	} `json:"interactive"`
}

// WebhookPayload represents the incoming webhook from WhatsApp
type WebhookPayload struct {
	Object string `json:"object"`
	Entry  []struct {
		ID      string `json:"id"`
		Changes []struct {
			Value struct {
				MessagingProduct string `json:"messaging_product"`
				Metadata         struct {
					DisplayPhoneNumber string `json:"display_phone_number"`
					PhoneNumberID      string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
					WaID string `json:"wa_id"`
				} `json:"contacts"`
				Messages []struct {
					From      string `json:"from"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					Type      string `json:"type"`
					Text      struct {
						Body string `json:"body"`
					} `json:"text,omitempty"`
					Interactive struct {
						Type    string `json:"type"`
						ButtonReply struct {
							ID    string `json:"id"`
							Title string `json:"title"`
						} `json:"button_reply,omitempty"`
						ListReply struct {
							ID          string `json:"id"`
							Title       string `json:"title"`
							Description string `json:"description"`
						} `json:"list_reply,omitempty"`
					} `json:"interactive,omitempty"`
					Image struct {
						ID       string `json:"id"`
						MimeType string `json:"mime_type"`
						Caption  string `json:"caption"`
					} `json:"image,omitempty"`
					Document struct {
						ID       string `json:"id"`
						Filename string `json:"filename"`
						MimeType string `json:"mime_type"`
						Caption  string `json:"caption"`
					} `json:"document,omitempty"`
				} `json:"messages"`
			} `json:"value"`
			Field string `json:"field"`
		} `json:"changes"`
	} `json:"entry"`
}
