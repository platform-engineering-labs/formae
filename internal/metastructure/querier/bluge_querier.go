// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package querier

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/blugelabs/bluge"
	querystr "github.com/blugelabs/query_string"

	apimodel "github.com/platform-engineering-labs/formae/internal/api/model"
	"github.com/platform-engineering-labs/formae/internal/metastructure/datastore"
	"github.com/platform-engineering-labs/formae/internal/metastructure/forma_command"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
)

type BlugeQuerier struct {
	datastore datastore.Datastore
}

func NewBlugeQuerier(datastore datastore.Datastore) *BlugeQuerier {
	return &BlugeQuerier{
		datastore: datastore,
	}
}

func (b *BlugeQuerier) QueryStatus(queryString string, clientID string, n int) ([]*forma_command.FormaCommand, error) {
	if queryString == "" {
		return b.datastore.QueryFormaCommands(&datastore.StatusQuery{N: n})
	}

	statusQuery, err := b.statusQuery(queryString, clientID, n)
	if err != nil {
		return nil, err
	}

	return b.datastore.QueryFormaCommands(statusQuery)
}

func (b *BlugeQuerier) statusQuery(queryString string, clientID string, n int) (*datastore.StatusQuery, error) {
	q, err := querystr.ParseQueryString(queryString, querystr.QueryStringOptions{})
	if err != nil {
		return nil, apimodel.InvalidQueryError{Reason: err.Error()}
	}
	statusQuery, err := b.translateToStatusQuery(q, clientID)
	if err != nil {
		return nil, apimodel.InvalidQueryError{Reason: err.Error()}
	}
	statusQuery.N = n

	return statusQuery, nil
}

func (b *BlugeQuerier) translateToStatusQuery(blugeQuery bluge.Query, clientID string) (*datastore.StatusQuery, error) {
	statusQuery := &datastore.StatusQuery{}
	err := b.processStatusQueryNode(blugeQuery, statusQuery, clientID, datastore.Required)
	if err != nil {
		return nil, err
	}

	return statusQuery, nil
}

func (b *BlugeQuerier) processStatusQueryNode(q bluge.Query, sq *datastore.StatusQuery, clientID string, constraint datastore.QueryItemConstraint) error {
	switch v := q.(type) {
	case *bluge.BooleanQuery:
		for _, mustQuery := range v.Musts() {
			if err := b.processStatusQueryNode(mustQuery, sq, clientID, datastore.Required); err != nil {
				return err
			}
		}
		for _, shouldQuery := range v.Shoulds() {
			if err := b.processStatusQueryNode(shouldQuery, sq, clientID, datastore.Optional); err != nil {
				return err
			}
		}
		for _, mustNotQuery := range v.MustNots() {
			if err := b.processStatusQueryNode(mustNotQuery, sq, clientID, datastore.Excluded); err != nil {
				return err
			}
		}
		return nil
	case *bluge.MatchQuery:
		return b.assignTermToStatusQuery(v.Field(), v.Match(), sq, clientID, constraint)
	default:
		return fmt.Errorf("unsupported query type: %T", q)
	}
}

func (b *BlugeQuerier) assignTermToStatusQuery(field string, value any, sq *datastore.StatusQuery, clientID string, constraint datastore.QueryItemConstraint) error {
	if field == "" {
		return fmt.Errorf("query term '%s' must have an explicit field", value)
	}

	switch strings.ToLower(field) {
	case "id":
		sq.CommandID = queryItem(value.(string), constraint)
	case "client":
		if value == "me" {
			value = clientID
		}
		sq.ClientID = queryItem(value.(string), constraint)
	case "command":
		sq.Command = queryItem(value.(string), constraint)
	case "status":
		sq.Status = queryItem(value.(string), constraint)
	case "stack":
		sq.Stack = queryItem(value.(string), constraint)
	case "managed":
		sq.Managed = queryItem(value.(bool), constraint)
	default:
		return fmt.Errorf("unknown field for StatusQuery: '%s'", field)
	}
	return nil
}

func queryItem[T any](value T, constraint datastore.QueryItemConstraint) *datastore.QueryItem[T] {
	return &datastore.QueryItem[T]{
		Item:       value,
		Constraint: constraint,
	}
}

func (b *BlugeQuerier) QueryResources(queryString string) ([]*pkgmodel.Resource, error) {
	// colons are used in resource types and byte query string syntax
	if strings.Contains(queryString, "::") {
		queryString = strings.ReplaceAll(queryString, "::", "\\:\\:")
	}

	var resourceQuery *datastore.ResourceQuery
	var err error
	if queryString == "" {
		resourceQuery = &datastore.ResourceQuery{}
	} else {
		resourceQuery, err = b.resourceQuery(queryString)
		if err != nil {
			return nil, err
		}
	}

	if resourceQuery == nil {
		return []*pkgmodel.Resource{}, nil
	}

	return b.datastore.QueryResources(resourceQuery)
}

func (b *BlugeQuerier) resourceQuery(queryString string) (*datastore.ResourceQuery, error) {
	q, err := querystr.ParseQueryString(queryString, querystr.QueryStringOptions{})
	if err != nil {
		return nil, apimodel.InvalidQueryError{Reason: err.Error()}
	}

	resourceQuery, err := b.translateToResourceQuery(q)
	if err != nil {
		return nil, apimodel.InvalidQueryError{Reason: err.Error()}
	}

	return resourceQuery, nil
}

func (b *BlugeQuerier) translateToResourceQuery(blugeQuery bluge.Query) (*datastore.ResourceQuery, error) {
	resourceQuery := &datastore.ResourceQuery{}
	err := b.processResourceQueryNode(blugeQuery, resourceQuery, datastore.Required)
	if err != nil {
		return nil, err
	}

	return resourceQuery, nil
}

func (b *BlugeQuerier) processResourceQueryNode(q bluge.Query, rq *datastore.ResourceQuery, constraint datastore.QueryItemConstraint) error {
	switch v := q.(type) {
	case *bluge.BooleanQuery:
		for _, mustQuery := range v.Musts() {
			if err := b.processResourceQueryNode(mustQuery, rq, datastore.Required); err != nil {
				return err
			}
		}
		for _, shouldQuery := range v.Shoulds() {
			if err := b.processResourceQueryNode(shouldQuery, rq, datastore.Optional); err != nil {
				return err
			}
		}
		for _, mustNotQuery := range v.MustNots() {
			if err := b.processResourceQueryNode(mustNotQuery, rq, datastore.Excluded); err != nil {
				return err
			}
		}
		return nil
	case *bluge.MatchQuery:
		return b.assignTermToResourceQuery(v.Field(), v.Match(), rq, constraint)
	default:
		return fmt.Errorf("unsupported query type: %T", q)
	}
}

func (b *BlugeQuerier) assignTermToResourceQuery(field string, value any, rq *datastore.ResourceQuery, constraint datastore.QueryItemConstraint) error {
	if field == "" {
		return fmt.Errorf("query term '%s' must have an explicit field", value)
	}

	switch strings.ToLower(field) {
	case "stack":
		rq.Stack = queryItem(value.(string), constraint)
	case "type":
		rq.Type = queryItem(value.(string), constraint)
	case "label":
		rq.Label = queryItem(value.(string), constraint)
	case "managed":
		boolVal, err := strconv.ParseBool(fmt.Sprintf("%v", value))
		if err != nil {
			return fmt.Errorf("invalid boolean value for 'managed': %v", err)
		}
		rq.Managed = queryItem(boolVal, constraint)
	default:
		return fmt.Errorf("unknown field for ResourceQuery: '%s'", field)
	}
	return nil
}
