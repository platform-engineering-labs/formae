// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
	"github.com/platform-engineering-labs/formae/pkg/plugin/resource"
)

// Every coordinate this fixture needs comes from the environment the agent
// was started with, because none of them exist until the run that provisions
// them. `formae connect` makes the trust, and only then is there an audience
// to mint for or a role to assume.
const (
	// audienceEnv names the audience the plugin asks its broker to mint for.
	// A token carries exactly one, and each cloud spells it differently: AWS
	// wants sts.amazonaws.com, GCP wants the workload identity provider's own
	// resource name.
	audienceEnv = "E2E_OIDC_AUDIENCE"

	// assumeRoleARNEnv and gcpProjectEnv each select one cloud's exchange, and
	// carry the one coordinate that exchange needs beyond the audience.
	assumeRoleARNEnv = "E2E_OIDC_ASSUME_ROLE_ARN"
	gcpProjectEnv    = "E2E_OIDC_GCP_PROJECT"

	// namespace this plugin serves, as its manifest declares it. Named in the
	// recorded failure so a test can tell whose pairing was missing.
	namespace = "OidcEcho"

	// assumeRoleSessionName labels the STS session. Stable, so the assumed
	// role ARN a test reads back is stable too.
	assumeRoleSessionName = "e2e-oidc-echo"

	// awsRegion is where the AWS STS call is made.
	awsRegion = "us-west-2"
)

// exchange is the cloud-specific half of the probe: what the token is spent
// on once the broker has minted it.
//
// An agent runs exactly one, chosen by which coordinates the test put in its
// environment, because a token is minted for one audience and is accepted by
// one exchange only.
type exchange interface {
	// Spend exchanges the identity token for real credentials and reports
	// proof of it: who the caller became, and how long the credentials last.
	//
	// The credentials themselves are deliberately not returned. They would
	// land in the resource's properties, which is not a place credentials
	// belong.
	Spend(ctx context.Context, token string) (identity, expiration string, err error)
}

// resolveExchange picks the exchange the environment configured. Naming both
// variables in the failure matters: the symptom of setting neither is a token
// that was minted and then not spent, which looks like the exchange failing
// rather than never having been asked for.
func resolveExchange() (exchange, error) {
	roleARN := os.Getenv(assumeRoleARNEnv)
	project := os.Getenv(gcpProjectEnv)

	switch {
	case roleARN != "" && project != "":
		return nil, fmt.Errorf("%s and %s are both set, so which cloud to exchange at is ambiguous",
			assumeRoleARNEnv, gcpProjectEnv)
	case roleARN != "":
		return awsExchange{roleARN: roleARN}, nil
	case project != "":
		return gcpExchange{audience: os.Getenv(audienceEnv), project: project}, nil
	default:
		return nil, fmt.Errorf("neither %s nor %s is set, so there is nothing to exchange the token at",
			assumeRoleARNEnv, gcpProjectEnv)
	}
}

// EchoPlugin serves OidcEcho::Tokens::Token. It holds no state beyond the
// token source the SDK installs, which resolves the paired broker per call.
type EchoPlugin struct {
	tokens plugin.OidcTokenSource
}

// SetOidcTokenSource satisfies plugin.OidcAware: the SDK calls it once at
// startup with the source every operation mints through.
func (p *EchoPlugin) SetOidcTokenSource(src plugin.OidcTokenSource) {
	p.tokens = src
}

func (p *EchoPlugin) RateLimit() pkgmodel.RateLimitConfig {
	return pkgmodel.RateLimitConfig{
		Scope:                            pkgmodel.RateLimitScopeNamespace,
		MaxRequestsPerSecondForNamespace: 5,
	}
}

func (p *EchoPlugin) DiscoveryFilters() []pkgmodel.MatchFilter { return nil }

func (p *EchoPlugin) LabelConfig() pkgmodel.LabelConfig {
	return pkgmodel.LabelConfig{DefaultQuery: "$.probeLabel"}
}

// tokenProperties asks the broker for a token, spends it at the configured
// exchange, and renders the resource's properties around both outcomes. A
// failure is recorded in tokenError or exchangeError rather than failed
// outright, so a test asserting the unpaired case reads an exact message
// instead of whatever an operator error renders to. Real plugins fail closed
// here; this one is only proving the wiring.
func (p *EchoPlugin) tokenProperties(ctx context.Context, probeLabel string) (json.RawMessage, error) {
	var token, tokenError string

	switch audience := os.Getenv(audienceEnv); {
	case audience == "":
		tokenError = fmt.Sprintf("%s is not set, so there is no audience to mint for", audienceEnv)
	case p.tokens == nil:
		// The SDK installs the source on every OidcAware plugin, so this only
		// fires if that wiring broke.
		tokenError = fmt.Sprintf("no oidc token source installed: namespace %s", namespace)
	default:
		minted, err := p.tokens.IdentityToken(ctx, audience)
		if err != nil {
			tokenError = fmt.Sprintf("%s: namespace %s", err, namespace)
		} else {
			token = minted
		}
	}

	// With no token there is nothing to spend, so the exchange outputs stay
	// empty rather than carrying the error of a call that was never worth
	// making.
	var identity, expiration, exchangeError string
	if token != "" {
		identity, expiration, exchangeError = spend(ctx, token)
	}

	return json.Marshal(map[string]string{
		"probeLabel":         probeLabel,
		"token":              token,
		"tokenError":         tokenError,
		"exchangeIdentity":   identity,
		"exchangeExpiration": expiration,
		"exchangeError":      exchangeError,
	})
}

// spend runs the configured exchange and flattens its outcome into the three
// strings the properties document carries.
func spend(ctx context.Context, token string) (identity, expiration, exchangeError string) {
	ex, err := resolveExchange()
	if err != nil {
		return "", "", err.Error()
	}
	identity, expiration, err = ex.Spend(ctx, token)
	if err != nil {
		return "", "", err.Error()
	}
	return identity, expiration, ""
}

// awsExchange trades the identity token for role credentials at AWS STS. The
// role is the one `formae connect` provisioned, and its trust policy pins the
// broker's issuer, subject and audience, so a token STS accepts here is proof
// the whole chain agrees.
type awsExchange struct {
	roleARN string
}

// assumeRolePropagation bounds how long the exchange keeps trying a role that
// is not assumable yet, and how often.
//
// A role created moments ago is not immediately assumable: IAM propagates
// asynchronously and STS answers AccessDenied in the meantime. That is the
// ordinary state of a role `formae connect` made seconds earlier, so reporting
// the first refusal would fail the run over a trust policy that is correct.
//
// The window is a child deadline, not a wall-clock check between attempts. A
// check between attempts bounds when the last request may *start*, not when it
// may finish, so a slow attempt begun just inside the window can run past the
// agent's 60s plugin call deadline and turn a recorded exchangeError into an
// operation timeout — which reads as the harness breaking rather than as the
// exchange refusing. The margin below leaves the operator's deadline room to
// be the one that never fires.
const (
	assumeRolePropagationWindow   = 30 * time.Second
	assumeRolePropagationInterval = 2 * time.Second
)

func (e awsExchange) Spend(ctx context.Context, token string) (identity, expiration string, err error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(awsRegion))
	if err != nil {
		return "", "", fmt.Errorf("loading aws config: %w", err)
	}
	client := sts.NewFromConfig(cfg)

	// Bounding the requests themselves, so nothing this loop starts can outlive
	// the window.
	ctx, cancel := context.WithTimeout(ctx, assumeRolePropagationWindow)
	defer cancel()

	for {
		out, err := client.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
			RoleArn:          aws.String(e.roleARN),
			RoleSessionName:  aws.String(assumeRoleSessionName),
			WebIdentityToken: aws.String(token),
		})
		if err == nil {
			if out.AssumedRoleUser != nil && out.AssumedRoleUser.Arn != nil {
				identity = *out.AssumedRoleUser.Arn
			}
			if out.Credentials != nil && out.Credentials.Expiration != nil {
				expiration = out.Credentials.Expiration.Format(time.RFC3339)
			}
			return identity, expiration, nil
		}
		// Only the refusal propagation produces is worth waiting out. A
		// malformed ARN, a rejected token or an expired credential is settled
		// on the first answer, and spending the whole window on it buries the
		// reason under a delay that looks like a hang.
		if !awaitingPropagation(err) {
			return "", "", fmt.Errorf("assume role with web identity: %w", err)
		}
		select {
		case <-ctx.Done():
			return "", "", fmt.Errorf("assume role with web identity: %w", err)
		case <-time.After(assumeRolePropagationInterval):
		}
	}
}

// awaitingPropagation reports whether err is the refusal STS gives for a role
// whose trust policy has not propagated yet.
//
// AccessDenied is also what a genuinely wrong trust policy produces, and the
// two are not distinguishable from the outside — which is the whole reason the
// window exists rather than a poll for readiness. Everything else is a settled
// answer and is returned as it stands.
func awaitingPropagation(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && api.ErrorCode() == "AccessDenied"
}

// probeLabelOf reads the probeLabel property out of a properties document.
func probeLabelOf(properties json.RawMessage) (string, error) {
	var props struct {
		ProbeLabel string `json:"probeLabel"`
	}
	if len(properties) == 0 {
		return "", fmt.Errorf("no properties supplied")
	}
	if err := json.Unmarshal(properties, &props); err != nil {
		return "", fmt.Errorf("parsing properties: %w", err)
	}
	if props.ProbeLabel == "" {
		return "", fmt.Errorf("probeLabel is required")
	}
	return props.ProbeLabel, nil
}

func (p *EchoPlugin) Create(ctx context.Context, req *resource.CreateRequest) (*resource.CreateResult, error) {
	probeLabel, err := probeLabelOf(req.Properties)
	if err != nil {
		return &resource.CreateResult{ProgressResult: failure(resource.OperationCreate, err)}, nil
	}

	properties, err := p.tokenProperties(ctx, probeLabel)
	if err != nil {
		return &resource.CreateResult{ProgressResult: failure(resource.OperationCreate, err)}, nil
	}

	return &resource.CreateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationCreate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           probeLabel,
			ResourceProperties: properties,
		},
	}, nil
}

// Read reports the same shape of document Create did, minting a fresh token
// and exchanging it again: there is no store behind this resource, so every
// read redoes the work.
func (p *EchoPlugin) Read(ctx context.Context, req *resource.ReadRequest) (*resource.ReadResult, error) {
	properties, err := p.tokenProperties(ctx, req.NativeID)
	if err != nil {
		return &resource.ReadResult{
			ResourceType: req.ResourceType,
			ErrorCode:    resource.OperationErrorCodeInternalFailure,
		}, nil
	}

	return &resource.ReadResult{
		ResourceType: req.ResourceType,
		Properties:   string(properties),
	}, nil
}

func (p *EchoPlugin) Update(ctx context.Context, req *resource.UpdateRequest) (*resource.UpdateResult, error) {
	properties, err := p.tokenProperties(ctx, req.NativeID)
	if err != nil {
		return &resource.UpdateResult{ProgressResult: failure(resource.OperationUpdate, err)}, nil
	}

	return &resource.UpdateResult{
		ProgressResult: &resource.ProgressResult{
			Operation:          resource.OperationUpdate,
			OperationStatus:    resource.OperationStatusSuccess,
			NativeID:           req.NativeID,
			ResourceProperties: properties,
		},
	}, nil
}

// Delete has nothing to delete: the resource never existed anywhere.
func (p *EchoPlugin) Delete(_ context.Context, req *resource.DeleteRequest) (*resource.DeleteResult, error) {
	return &resource.DeleteResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationDelete,
			OperationStatus: resource.OperationStatusSuccess,
			NativeID:        req.NativeID,
		},
	}, nil
}

// Status is never polled: every operation finishes synchronously.
func (p *EchoPlugin) Status(_ context.Context, req *resource.StatusRequest) (*resource.StatusResult, error) {
	return &resource.StatusResult{
		ProgressResult: &resource.ProgressResult{
			Operation:       resource.OperationCheckStatus,
			OperationStatus: resource.OperationStatusSuccess,
			RequestID:       req.RequestID,
			NativeID:        req.NativeID,
		},
	}, nil
}

// List returns nothing: the schema marks the resource type undiscoverable.
func (p *EchoPlugin) List(_ context.Context, _ *resource.ListRequest) (*resource.ListResult, error) {
	return &resource.ListResult{}, nil
}

// failure renders an operation failure carrying err's message.
func failure(operation resource.Operation, err error) *resource.ProgressResult {
	return &resource.ProgressResult{
		Operation:       operation,
		OperationStatus: resource.OperationStatusFailure,
		ErrorCode:       resource.OperationErrorCodeInvalidRequest,
		StatusMessage:   err.Error(),
	}
}

// gcpExchange trades the identity token for a federated access token at
// Google's STS, then spends that token reading the project.
//
// The two steps are both needed and prove different things. The exchange
// proves the workload identity provider `formae connect` created trusts the
// broker's issuer and accepts its subject and audience; the project read
// proves connect also granted that federated principal something, which is
// the half a successful exchange alone would not show.
//
// It is written against the REST endpoints rather than the Google SDK because
// the SDK's federation support wants a credential-configuration file naming a
// token source on disk, and the token here arrives in memory from the broker.
type gcpExchange struct {
	// audience is the workload identity provider's full resource name, which
	// is both what the token was minted for and what the exchange is
	// addressed to. Google pins the provider's allowed audiences to this same
	// string, so the two cannot drift.
	audience string
	project  string
}

// gcpTokenExchangeURL and gcpProjectURL are Google's STS token endpoint and
// the project read used as proof the exchanged credentials work.
const (
	gcpTokenExchangeURL = "https://sts.googleapis.com/v1/token"
	gcpProjectURL       = "https://cloudresourcemanager.googleapis.com/v1/projects/"
)

func (e gcpExchange) Spend(ctx context.Context, token string) (identity, expiration string, err error) {
	if e.audience == "" {
		return "", "", fmt.Errorf("%s is not set, so there is no workload identity provider to exchange at", audienceEnv)
	}

	accessToken, lifetime, err := e.federate(ctx, token)
	if err != nil {
		return "", "", err
	}

	number, err := e.readProjectNumber(ctx, accessToken)
	if err != nil {
		return "", "", err
	}

	// The identity is reported as the project the credentials could actually
	// read, for the same reason the AWS side reports the assumed role ARN:
	// a token that exchanged but reaches nothing has not proven access.
	return "projects/" + number, time.Now().Add(lifetime).UTC().Format(time.RFC3339), nil
}

// federate performs the RFC 8693 token exchange and returns the access token
// and how long it lasts.
func (e gcpExchange) federate(ctx context.Context, token string) (string, time.Duration, error) {
	body, err := json.Marshal(map[string]string{
		"audience":           e.audience,
		"grantType":          "urn:ietf:params:oauth:grant-type:token-exchange",
		"requestedTokenType": "urn:ietf:params:oauth:token-type:access_token",
		"scope":              "https://www.googleapis.com/auth/cloud-platform",
		"subjectTokenType":   "urn:ietf:params:oauth:token-type:jwt",
		"subjectToken":       token,
	})
	if err != nil {
		return "", 0, fmt.Errorf("building the token exchange request: %w", err)
	}

	data, err := gcpPost(ctx, gcpTokenExchangeURL, "", body)
	if err != nil {
		return "", 0, fmt.Errorf("exchanging the identity token: %w", err)
	}

	var exchanged struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &exchanged); err != nil {
		return "", 0, fmt.Errorf("parsing the token exchange response: %w", err)
	}
	if exchanged.AccessToken == "" {
		return "", 0, fmt.Errorf("the token exchange returned no access token")
	}
	return exchanged.AccessToken, time.Duration(exchanged.ExpiresIn) * time.Second, nil
}

// readProjectNumber spends the federated access token on the one read that
// shows it carries access, and returns what it read back.
func (e gcpExchange) readProjectNumber(ctx context.Context, accessToken string) (string, error) {
	data, err := gcpGet(ctx, gcpProjectURL+e.project, accessToken)
	if err != nil {
		return "", fmt.Errorf("reading project %s with the exchanged credentials: %w", e.project, err)
	}

	var project struct {
		ProjectNumber string `json:"projectNumber"`
	}
	if err := json.Unmarshal(data, &project); err != nil {
		return "", fmt.Errorf("parsing the project read response: %w", err)
	}
	if project.ProjectNumber == "" {
		return "", fmt.Errorf("the project read returned no project number")
	}
	return project.ProjectNumber, nil
}

func gcpPost(ctx context.Context, url, accessToken string, body []byte) ([]byte, error) {
	return gcpDo(ctx, http.MethodPost, url, accessToken, body)
}

func gcpGet(ctx context.Context, url, accessToken string) ([]byte, error) {
	return gcpDo(ctx, http.MethodGet, url, accessToken, nil)
}

// gcpDo makes one call and returns the body. A non-2xx carries Google's own
// error text, capped: the whole value of this fixture on a failing run is the
// reason Google gave, and a refusal at the exchange and a refusal at the read
// are different problems that read almost identically without it.
func gcpDo(ctx context.Context, method, url, accessToken string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	return data, nil
}
