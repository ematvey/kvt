package mcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/service"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const howtoURI = "kvt://howto"

type Server struct {
	sdk       *mcpsdk.Server
	toolNames map[string]bool
}

func NewServer(svc *service.Service, cfg config.Config) (*Server, error) {
	if svc == nil {
		return nil, fmt.Errorf("service is required")
	}
	server := &Server{
		sdk: mcpsdk.NewServer(&mcpsdk.Implementation{
			Name:    "kvt",
			Version: "dev",
		}, &mcpsdk.ServerOptions{Instructions: DefaultInstructions()}),
		toolNames: map[string]bool{},
	}
	registerTools(server, svc, cfg)
	registerHowto(server)
	return server, nil
}

func (s *Server) Run(ctx context.Context, transport mcpsdk.Transport) error {
	return s.sdk.Run(ctx, transport)
}

func (s *Server) RunStdio(ctx context.Context) error {
	return s.Run(ctx, &mcpsdk.StdioTransport{})
}

func (s *Server) SDK() *mcpsdk.Server {
	return s.sdk
}

func StreamableHTTPHandler(server *Server) http.Handler {
	return mcpsdk.NewStreamableHTTPHandler(func(*http.Request) *mcpsdk.Server {
		if server == nil {
			return nil
		}
		return server.SDK()
	}, nil)
}

func RegisteredToolNames(s *Server) map[string]bool {
	out := map[string]bool{}
	if s == nil {
		return out
	}
	for name, ok := range s.toolNames {
		out[name] = ok
	}
	return out
}

func registerHowto(server *Server) {
	server.sdk.AddResource(&mcpsdk.Resource{
		Name:        "kvt_howto",
		Title:       "KVT howto",
		URI:         howtoURI,
		MIMEType:    "text/markdown",
		Description: "Agent guidance for reading and writing KVT OKF files.",
	}, func(context.Context, *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
		return &mcpsdk.ReadResourceResult{
			Contents: []*mcpsdk.ResourceContents{{
				URI:      howtoURI,
				MIMEType: "text/markdown",
				Text:     DefaultHowto(),
			}},
		}, nil
	})
	server.sdk.AddPrompt(&mcpsdk.Prompt{
		Name:        "kvt_howto",
		Title:       "KVT howto",
		Description: "How to use KVT tools safely and keep OKF files valid.",
	}, func(context.Context, *mcpsdk.GetPromptRequest) (*mcpsdk.GetPromptResult, error) {
		return &mcpsdk.GetPromptResult{
			Description: "KVT agent guidance",
			Messages: []*mcpsdk.PromptMessage{{
				Role:    mcpsdk.Role("user"),
				Content: &mcpsdk.TextContent{Text: DefaultHowto()},
			}},
		}, nil
	})
}
