// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package usage

import (
	"reflect"
	"time"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/logging"
	posthog "github.com/posthog/posthog-go"
)

const (
	APIKey      = "phc_4lcMdGyCWr8QKaPaWApjxAmrSv7hJidtVs7KP8e3Svx"
	APIEndpoint = "https://us.i.posthog.com"
)

type PostHogSender struct {
}

func NewPostHogSender() (Sender, error) {
	return PostHogSender{}, nil
}

func (s PostHogSender) SendStats(stats *apimodel.Stats, devMode bool) error {
	client, err := posthog.NewWithConfig(APIKey, posthog.Config{Endpoint: APIEndpoint, Logger: &logging.PosthogLogger{}})
	if err == nil && client != nil {
		defer func() { _ = client.Close() }()

		event := posthog.Capture{
			DistinctId: stats.AgentID,
			Event:      "stats",
			Timestamp:  time.Now(),
			Properties: map[string]any{
				"$process_person_profile": false,
				"dev_mode":                devMode,
			},
		}

		statsValue := reflect.ValueOf(stats).Elem()
		statsType := statsValue.Type()

		for i := 0; i < statsValue.NumField(); i++ {
			field := statsType.Field(i)
			key := field.Name
			value := statsValue.Field(i).Interface()
			event.Properties[key] = value
		}

		err = client.Enqueue(event)
	}

	return err
}
