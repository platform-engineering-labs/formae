// © 2025 Platform Engineering Labs Inc.
//
// SPDX-License-Identifier: FSL-1.1-ALv2

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	echoSwagger "github.com/swaggo/echo-swagger"

	_ "github.com/platform-engineering-labs/formae/docs"
	"github.com/platform-engineering-labs/formae/internal/logging"
	"github.com/platform-engineering-labs/formae/internal/metastructure"
	"github.com/platform-engineering-labs/formae/internal/metastructure/config"
	apimodel "github.com/platform-engineering-labs/formae/pkg/api/model"
	pkgmodel "github.com/platform-engineering-labs/formae/pkg/model"
	"github.com/platform-engineering-labs/formae/pkg/plugin"
)

const (
	BasePath               = "/api/v1"
	CommandsRoute          = BasePath + "/commands"
	CommandStatusRoute     = BasePath + "/commands/:id/status"
	ListCommandStatusRoute = BasePath + "/commands/status"
	CancelCommandsRoute    = BasePath + "/commands/cancel"
	ListResourcesRoute     = BasePath + "/resources"
	ListTargetsRoute       = BasePath + "/targets"
	StatsRoute             = BasePath + "/stats"

	AdminBasePath = BasePath + "/admin"
	SyncRoute     = AdminBasePath + "/synchronize"
	DiscoverRoute = AdminBasePath + "/discover"

	HealthRoute  = BasePath + "/health"
	MetricsRoute = "/metrics"
	APIDocsRoute = "/swagger/*"
)

type Server struct {
	echo           *echo.Echo
	metastructure  metastructure.MetastructureAPI
	ctx            context.Context
	pluginManager  *plugin.Manager
	serverConfig   *pkgmodel.ServerConfig
	pluginConfig   *pkgmodel.PluginConfig
	metricsHandler http.Handler
}

func NewServer(ctx context.Context, metastructure metastructure.MetastructureAPI, pluginManager *plugin.Manager, serverConfig *pkgmodel.ServerConfig, pluginConfig *pkgmodel.PluginConfig, metricsHandler http.Handler) *Server {
	server := &Server{
		metastructure:  metastructure,
		ctx:            ctx,
		pluginManager:  pluginManager,
		serverConfig:   serverConfig,
		pluginConfig:   pluginConfig,
		metricsHandler: metricsHandler,
	}

	server.echo = server.configureEcho()

	return server
}

func (s *Server) configureAuth() error {
	if s.pluginConfig.Authentication != nil {
		auth, err := s.pluginManager.AuthPlugin(s.pluginConfig.Authentication)
		if err != nil {
			return err
		}

		handler, err := (*auth).Handler(s.pluginConfig.Authentication)
		if err != nil {
			return err
		}

		s.echo.Use(echo.WrapMiddleware(handler))
	}

	return nil
}

// configureNetwork sets up the network listener by loading the appropriate network plugin based on the configuration.
func (s *Server) configureNetwork() (string, error) {
	if s.pluginConfig.Network != nil {
		net, err := s.pluginManager.NetworkPlugin(s.pluginConfig.Network)
		if err != nil {
			return "", err
		}

		s.echo.Listener, err = (*net).Listen(s.pluginConfig.Network, s.serverConfig.Port)
		if err != nil {
			return "", err
		}

		return "", nil
	}

	return fmt.Sprintf(":%d", s.serverConfig.Port), nil
}

// Start launches the server in a separate goroutine
func (s *Server) Start() {
	go func() {
		err := s.configureAuth()
		if err != nil {
			s.echo.Logger.Fatal(err)
			return
		}

		listen, err := s.configureNetwork()
		if err != nil {
			s.echo.Logger.Fatal(err)
			return
		}

		// TLS may only be enabled with no custom listener
		if listen != "" && s.serverConfig.TLSCert != "" && s.serverConfig.TLSKey != "" {
			if err := s.echo.StartTLS(listen, s.serverConfig.TLSCert, s.serverConfig.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.echo.Logger.Error(err)
			}
		} else {
			if err := s.echo.Start(listen); err != nil && !errors.Is(err, http.ErrServerClosed) {
				s.echo.Logger.Error(err)
			}
		}
	}()
	<-s.ctx.Done()
	s.Stop(false)
}

// Stop gracefully shuts down the server, waiting for ongoing requests to complete
func (s *Server) Stop(_ bool) {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	slog.Info("API server received shutdown")
	if err := s.echo.Shutdown(shutdownCtx); err != nil {
		slog.Info("API server error when shutting down", "error", err)
	}
	slog.Info("API Server successfully shutdown")
}

// @title Formae REST API
// @version 1.0
// @description This API allows you to programatically interact with Formae without the CLI.
// @host localhost:8080
// @BasePath /api/v1
func (s *Server) configureEcho() *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	e.Logger = logging.NewEchoLogger()
	e.StdLogger = log.Default()

	// Forma command endpoints
	e.POST(CommandsRoute, s.SubmitFormaCommand)
	e.GET(CommandStatusRoute, s.CommandStatus)
	e.GET(ListCommandStatusRoute, s.ListCommandStatus)
	e.POST(CancelCommandsRoute, s.CancelCommands)

	// Resource extraction endpoint
	e.GET(ListResourcesRoute, s.ListResources)

	// Target listing endpoint
	e.GET(ListTargetsRoute, s.ListTargets)

	// Usage stats endpoint
	e.GET(StatsRoute, s.Stats)

	// Health endpoint
	e.GET(HealthRoute, s.Health)

	// Admin endpoints
	e.POST(SyncRoute, s.ForceSync)
	e.POST(DiscoverRoute, s.ForceDiscover)

	// Prometheus metrics endpoint (if enabled)
	if s.metricsHandler != nil {
		e.GET(MetricsRoute, echo.WrapHandler(s.metricsHandler))
	}

	// API docs endpoint
	e.GET(APIDocsRoute, echoSwagger.WrapHandler)

	return e
}

// @Summary Submit a Forma command
// @Description Submits a Forma command to the agent. The command is executed asynchronously.
// @Tags commands
// @Accept multipart/form-data
// @Produce json
// @Param Client-ID header string true "Unique identifier for the client."
// @Param command formData string true "The command to execute, either apply or destroy."
// @Param mode formData string false "Only applies to the apply command. The desired command mode, either reconcile or patch."
// @Param simulate formData boolean false "If true, simulates command execution without actual changes to the infrastructure (defaults to false)."
// @Param force formData boolean false "Only applies to the apply command in reconcile mode. If true, any changes made to the infrastructure since the last reconcile, either by patches or outside of Formae, will be overwritten."
// @Param query formData string false "Only applies to destroy commands. A query string to select the resources to be destroyed."
// @Param file formData file false "A valid Forma file."
// @Success 202 {object} apimodel.SubmitCommandResponse "Accepted: The command is validated and stored and queued for execution."
// @Header 202 {string} string Location "The URL to poll for the command's execution status (e.g., /api/v1/commands/{command_id}/status)."
// @Failure 500 {string} string "Internal Server Error."
// @Router /commands [post]
func (s *Server) SubmitFormaCommand(c echo.Context) error {
	// Form data relevant to all commands
	clientID := c.Request().Header.Get("Client-ID")
	if clientID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Client-ID header is required")
	}
	command := c.FormValue("command")
	if command == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "command is required")
	}
	simulate := getOptionalBool(c, "simulate", false)

	var response *apimodel.SubmitCommandResponse

	switch command {
	case "apply":
		mode := pkgmodel.FormaApplyMode(c.FormValue("mode"))
		force := getOptionalBool(c, "force", false)
		if !hasFormaFile(c) {
			return echo.NewHTTPError(http.StatusBadRequest, "file form field is required for apply command")
		}
		forma, err := getForma(c)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}

		response, err = s.metastructure.ApplyForma(forma, &config.FormaCommandConfig{
			Mode:     mode,
			Simulate: simulate,
			Force:    force,
		}, clientID)
		if err != nil {
			return mapError(c, err)
		}
	case "destroy":
		var err error
		query := c.FormValue("query")
		if query != "" {
			// If query is provided, use it
			response, err = s.metastructure.DestroyByQuery(query, &config.FormaCommandConfig{Simulate: simulate}, clientID)
		} else {
			// Otherwise, expect a Forma file
			if !hasFormaFile(c) {
				return echo.NewHTTPError(http.StatusBadRequest, "either query form field or file form field is required for destroy command")
			}
			forma, getFormaErr := getForma(c)
			if getFormaErr != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, getFormaErr.Error())
			}
			response, err = s.metastructure.DestroyForma(forma, &config.FormaCommandConfig{Simulate: simulate}, clientID)
		}
		if err != nil {
			return mapError(c, err)
		}
	default:
		return fmt.Errorf("unsupported command: %s", command)
	}

	location := strings.Replace(CommandStatusRoute, ":id", response.CommandID, 1)
	c.Response().Header().Set("Location", location)

	return c.JSON(http.StatusAccepted, response)
}

// @Summary Get the status of a Forma command
// @Description Retrieves the status of a previously submitted Forma command using its ID.
// @Tags commands
// @Produce json
// @Param Client-ID header string true "Unique identifier for the client."
// @Param id path string true "The ID of the command."
// @Success 200 {object} apimodel.ListCommandStatusResponse "OK: The command's execution status."
// @Failure 400 {string} string "Bad Request: Missing or invalid parameters."
// @Failure 500 {string} string "Internal Server Error."
// @Router /command/{id}/status [get]
func (s *Server) CommandStatus(c echo.Context) error {
	clientID := c.Request().Header.Get("Client-ID")
	if clientID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Client-ID header is required")
	}
	id := c.QueryParam("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is required")
	}
	query := fmt.Sprintf("id:%s", id)

	return s.getCommandStatus(c, clientID, query, 1)
}

// @Summary Get the status of multiple Forma commands
// @Description Retrieves the statuses of multiple Forma commands based on a query string.
// @Tags commands
// @Produce json
// @Param Client-ID header string true "Unique identifier for the client."
// @Param query query string false "The query string to select the commands. If empty, retrieves the status of the most recent command."
// @Param max_results query string false "The maximum number of command statuses to return (default is 10)."
// @Success 200 {object} apimodel.ListCommandStatusResponse "OK: The commands' execution statuses."
// @Failure 400 {string} string "Bad Request: Missing or invalid parameters."
// @Failure 500 {string} string "Internal Server Error."
// @Router /commands/status [get]
func (s *Server) ListCommandStatus(c echo.Context) error {
	clientID := c.Request().Header.Get("Client-ID")
	if clientID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Client-ID header is required")
	}
	maxResults := c.QueryParam("max_results")
	query := c.QueryParam("query")
	if maxResults == "" {
		maxResults = "10"
	}
	n, err := strconv.Atoi(maxResults)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "N must be an integer")
	}

	return s.getCommandStatus(c, clientID, query, n)
}

// @Summary List resources
// @Description Extracts and lists resources based on a provided query string.
// @Tags resources
// @Produce json
// @Param Client-ID header string true "Unique identifier for the client."
// @Param query query string true "The query string to select the resources."
// @Success 200 {object} pkgmodel.Forma ": OK: The extracted resources."
// @Failure 400 {string} string "Bad Request: Invalid query."
// @Failure 404 {string} string "Not Found: No resources found matching the query."
// @Failure 406 {string} string "Not Acceptable: The request is not supported."
// @Failure 500 {string} string "Internal Server Error."
// @Router /resources [get]
func (s *Server) ListResources(c echo.Context) error {
	query := c.QueryParam("query")
	resources, err := s.metastructure.ExtractResources(query)
	if err != nil {
		return mapError(c, err)
	}
	if resources == nil || len(resources.Resources) == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("Resources cannot be extracted: %v", err),
		})
	}

	return c.JSON(http.StatusOK, resources)
}

// @Summary List targets
// @Description Retrieves targets based on query parameters
// @Tags targets
// @Produce json
// @Param query query string false "Query string to filter targets (e.g., 'namespace:aws discoverable:true')"
// @Success 200 {array} pkgmodel.Target "OK: List of targets."
// @Failure 404 {string} string "Not Found: No targets found."
// @Failure 500 {string} string "Internal Server Error."
// @Router /targets [get]
func (s *Server) ListTargets(c echo.Context) error {
	query := c.QueryParam("query")
	targets, err := s.metastructure.ExtractTargets(query)
	if err != nil {
		return mapError(c, err)
	}
	if len(targets) == 0 {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "No targets found",
		})
	}

	return c.JSON(http.StatusOK, targets)
}

// @Summary Get usage statistics
// @Description Retrieves usage statistics of the Formae agent.
// @Tags stats
// @Produce json
// @Success 200 {object} apimodel.Stats": OK: The usage statistics."
// @Failure 500 {string} string "Internal Server Error."
// @Router /stats [get]
func (s *Server) Stats(c echo.Context) error {
	stats, err := s.metastructure.Stats()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err)
	}

	return c.JSON(http.StatusOK, stats)
}

// @Summary Health Check
// @Description A simple health check endpoint to verify that the API server is running.
// @Tags health
// @Success 200
// @Router /health [get]
func (s *Server) Health(c echo.Context) error {
	return c.JSON(http.StatusOK, nil)
}

// @Summary Force resource synchronization
// @Description Triggers an immediate synchronization of the resource state with the actual infrastructure.
// @Tags admin
// @Success 200
// @Failure 500 {string} string "Internal Server Error."
// @Router /admin/synchronize [post]
func (s *Server) ForceSync(c echo.Context) error {
	err := s.metastructure.ForceSync()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	return c.JSON(http.StatusOK, "")
}

// @Summary Force resource discovery
// @Description Initiates an immediate discovery process to identify resources in the infrastructure.
// @Tags admin
// @Success 200
// @Failure 500 {string} string "Internal Server Error."
// @Router /admin/discover [post]
func (s *Server) ForceDiscover(c echo.Context) error {
	if err := s.metastructure.ForceDiscovery(); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	return c.JSON(http.StatusOK, "")
}

// getCommandStatus is a helper to retrieve command status and handle common error/status logic
func (s *Server) getCommandStatus(c echo.Context, clientID, query string, n int) error {
	result, err := s.metastructure.ListFormaCommandStatus(query, clientID, n)
	if err != nil {
		return mapError(c, err)
	}
	if len(result.Commands) == 0 {
		return echo.NewHTTPError(http.StatusNotFound)
	}

	return c.JSON(http.StatusOK, result)
}

// getOptionalBool retrieves a boolean form value, returning a default if not present or invalid
func getOptionalBool(c echo.Context, key string, defaultValue bool) bool {
	valStr := c.FormValue(key)

	if valStr == "" {
		return defaultValue
	}

	parsedVal, err := strconv.ParseBool(valStr)
	if err != nil {
		c.Logger().Warnf("Form field '%s' found with invalid boolean value '%s'. Defaulting to %t. Error: %v", key, valStr, defaultValue, err)
		return defaultValue
	}

	return parsedVal
}

// getForma extracts and parses the Forma file from the multipart form data
func getForma(c echo.Context) (*pkgmodel.Forma, error) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("file form field missing: %w", err)
	}

	src, err := fileHeader.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open file stream: %w", err)
	}
	defer func() {
		_ = src.Close()
	}()

	var forma pkgmodel.Forma

	decoder := json.NewDecoder(src)

	if err := decoder.Decode(&forma); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("file is empty")
		}
		// Catch JSON formatting errors
		return nil, fmt.Errorf("invalid JSON format in file: %w", err)
	}

	return &forma, nil
}

// hasFormaFile checks if the request has a file form field named "file"
func hasFormaFile(c echo.Context) bool {
	_, err := c.FormFile("file")
	return err == nil
}

// mapError maps metastructure errors to appropriate HTTP responses
func mapError(c echo.Context, err error) error {
	var reconcileRejectedResult apimodel.FormaReconcileRejectedError
	if errors.As(err, &reconcileRejectedResult) {
		return apiError(c, http.StatusConflict, apimodel.ReconcileRejected, reconcileRejectedResult)
	}

	var conflictingResourcesResult apimodel.FormaConflictingCommandsError
	if errors.As(err, &conflictingResourcesResult) {
		return apiError(c, http.StatusConflict, apimodel.ConflictingCommands, conflictingResourcesResult)
	}

	var cyclesDetectedResult apimodel.FormaCyclesDetectedError
	if errors.As(err, &cyclesDetectedResult) {
		return apiError(c, http.StatusBadRequest, apimodel.CyclesDetected, cyclesDetectedResult)
	}

	var rejectedResult apimodel.FormaPatchRejectedError
	if errors.As(err, &rejectedResult) {
		return apiError(c, http.StatusUnprocessableEntity, apimodel.PatchRejected, rejectedResult)
	}

	var resourceNotFoundError apimodel.FormaReferencedResourcesNotFoundError
	if errors.As(err, &resourceNotFoundError) {
		return apiError(c, http.StatusBadRequest, apimodel.ReferencedResourcesNotFound, resourceNotFoundError)
	}

	var targetExistsError apimodel.TargetAlreadyExistsError
	if errors.As(err, &targetExistsError) {
		return apiError(c, http.StatusConflict, apimodel.TargetAlreadyExists, targetExistsError)
	}

	var requiredFieldMissingError apimodel.RequiredFieldMissingOnCreateError
	if errors.As(err, &requiredFieldMissingError) {
		return apiError(c, http.StatusBadRequest, apimodel.RequiredFieldMissingOnCreate, requiredFieldMissingError)
	}

	var invalidQueryError apimodel.InvalidQueryError
	if errors.As(err, &invalidQueryError) {
		return apiError(c, http.StatusBadRequest, apimodel.InvalidQuery, invalidQueryError)
	}

	var stackReferenceNotFoundError apimodel.StackReferenceNotFoundError
	if errors.As(err, &stackReferenceNotFoundError) {
		return apiError(c, http.StatusBadRequest, apimodel.StackReferenceNotFound, stackReferenceNotFoundError)
	}

	var targetReferenceNotFoundError apimodel.TargetReferenceNotFoundError
	if errors.As(err, &targetReferenceNotFoundError) {
		return apiError(c, http.StatusBadRequest, apimodel.TargetReferenceNotFound, targetReferenceNotFoundError)
	}

	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return nil
}

// @Summary Cancel commands
// @Description Cancels commands that are currently in progress. Can cancel either the most recent command or commands matching a query.
// @Tags commands
// @Produce json
// @Param Client-ID header string true "Unique identifier for the client."
// @Param query query string false "Optional query string to select commands to cancel. If not provided, cancels the most recent command."
// @Success 202 {object} apimodel.CancelCommandResponse "Accepted: Commands are being canceled."
// @Success 404 {string} string "Not Found: No in-progress commands found to cancel."
// @Failure 400 {string} string "Bad Request: Invalid query or missing Client-ID."
// @Failure 500 {string} string "Internal Server Error."
// @Router /commands/cancel [post]
func (s *Server) CancelCommands(c echo.Context) error {
	clientID := c.Request().Header.Get("Client-ID")
	if clientID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Client-ID header is required")
	}

	query := c.QueryParam("query")

	result, err := s.metastructure.CancelCommandsByQuery(query, clientID)
	if err != nil {
		return mapError(c, err)
	}

	if result == nil || len(result.CommandIDs) == 0 {
		return c.NoContent(http.StatusNotFound)
	}

	return c.JSON(http.StatusAccepted, result)
}

// apiError is a helper to wrap error data in ErrorResponse[T] and return as json
func apiError[T any](c echo.Context, status int, errorType apimodel.APIError, data T) error {
	return c.JSON(status, apimodel.ErrorResponse[T]{
		ErrorType: errorType,
		Data:      data,
	})
}
