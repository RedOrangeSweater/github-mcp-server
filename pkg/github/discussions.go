package github

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/go-viper/mapstructure/v2"
	"github.com/google/go-github/v79/github"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shurcooL/githubv4"
)

const DefaultGraphQLPageSize = 30

// Common interface for all discussion query types
type DiscussionQueryResult interface {
	GetDiscussionFragment() DiscussionFragment
}

// Implement the interface for all query types
func (q *BasicNoOrder) GetDiscussionFragment() DiscussionFragment {
	return q.Repository.Discussions
}

func (q *BasicWithOrder) GetDiscussionFragment() DiscussionFragment {
	return q.Repository.Discussions
}

func (q *WithCategoryAndOrder) GetDiscussionFragment() DiscussionFragment {
	return q.Repository.Discussions
}

func (q *WithCategoryNoOrder) GetDiscussionFragment() DiscussionFragment {
	return q.Repository.Discussions
}

type DiscussionFragment struct {
	Nodes      []NodeFragment
	PageInfo   PageInfoFragment
	TotalCount githubv4.Int
}

type NodeFragment struct {
	Number         githubv4.Int
	Title          githubv4.String
	CreatedAt      githubv4.DateTime
	UpdatedAt      githubv4.DateTime
	Closed         githubv4.Boolean
	IsAnswered     githubv4.Boolean
	AnswerChosenAt *githubv4.DateTime
	Author         struct {
		Login githubv4.String
	}
	Category struct {
		Name githubv4.String
	} `graphql:"category"`
	URL githubv4.String `graphql:"url"`
}

type PageInfoFragment struct {
	HasNextPage     bool
	HasPreviousPage bool
	StartCursor     githubv4.String
	EndCursor       githubv4.String
}

type BasicNoOrder struct {
	Repository struct {
		Discussions DiscussionFragment `graphql:"discussions(first: $first, after: $after)"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

type BasicWithOrder struct {
	Repository struct {
		Discussions DiscussionFragment `graphql:"discussions(first: $first, after: $after, orderBy: { field: $orderByField, direction: $orderByDirection })"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

type WithCategoryAndOrder struct {
	Repository struct {
		Discussions DiscussionFragment `graphql:"discussions(first: $first, after: $after, categoryId: $categoryId, orderBy: { field: $orderByField, direction: $orderByDirection })"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

type WithCategoryNoOrder struct {
	Repository struct {
		Discussions DiscussionFragment `graphql:"discussions(first: $first, after: $after, categoryId: $categoryId)"`
	} `graphql:"repository(owner: $owner, name: $repo)"`
}

func fragmentToDiscussion(fragment NodeFragment) *github.Discussion {
	return &github.Discussion{
		Number:    github.Ptr(int(fragment.Number)),
		Title:     github.Ptr(string(fragment.Title)),
		HTMLURL:   github.Ptr(string(fragment.URL)),
		CreatedAt: &github.Timestamp{Time: fragment.CreatedAt.Time},
		UpdatedAt: &github.Timestamp{Time: fragment.UpdatedAt.Time},
		User: &github.User{
			Login: github.Ptr(string(fragment.Author.Login)),
		},
		DiscussionCategory: &github.DiscussionCategory{
			Name: github.Ptr(string(fragment.Category.Name)),
		},
	}
}

func getQueryType(useOrdering bool, categoryID *githubv4.ID) any {
	if categoryID != nil && useOrdering {
		return &WithCategoryAndOrder{}
	}
	if categoryID != nil && !useOrdering {
		return &WithCategoryNoOrder{}
	}
	if categoryID == nil && useOrdering {
		return &BasicWithOrder{}
	}
	return &BasicNoOrder{}
}

func ListDiscussions(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "list_discussions",
			Description: t("TOOL_LIST_DISCUSSIONS_DESCRIPTION", "List discussions for a repository or organisation."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_LIST_DISCUSSIONS_USER_TITLE", "List discussions"),
				ReadOnlyHint: true,
			},
			InputSchema: WithCursorPagination(&jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name. If not provided, discussions will be queried at the organisation level.",
					},
					"category": {
						Type:        "string",
						Description: "Optional filter by discussion category ID. If provided, only discussions with this category are listed.",
					},
					"orderBy": {
						Type:        "string",
						Description: "Order discussions by field. If provided, the 'direction' also needs to be provided.",
						Enum:        []any{"CREATED_AT", "UPDATED_AT"},
					},
					"direction": {
						Type:        "string",
						Description: "Order direction.",
						Enum:        []any{"ASC", "DESC"},
					},
				},
				Required: []string{"owner"},
			}),
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			owner, err := RequiredParam[string](args, "owner")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			repo, err := OptionalParam[string](args, "repo")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			// when not provided, default to the .github repository
			// this will query discussions at the organisation level
			if repo == "" {
				repo = ".github"
			}

			category, err := OptionalParam[string](args, "category")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			orderBy, err := OptionalParam[string](args, "orderBy")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			direction, err := OptionalParam[string](args, "direction")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			// Get pagination parameters and convert to GraphQL format
			pagination, err := OptionalCursorPaginationParams(args)
			if err != nil {
				return nil, nil, err
			}
			paginationParams, err := pagination.ToGraphQLParams()
			if err != nil {
				return nil, nil, err
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			var categoryID *githubv4.ID
			if category != "" {
				id := githubv4.ID(category)
				categoryID = &id
			}

			vars := map[string]interface{}{
				"owner": githubv4.String(owner),
				"repo":  githubv4.String(repo),
				"first": githubv4.Int(*paginationParams.First),
			}
			if paginationParams.After != nil {
				vars["after"] = githubv4.String(*paginationParams.After)
			} else {
				vars["after"] = (*githubv4.String)(nil)
			}

			// this is an extra check in case the tool description is misinterpreted, because
			// we shouldn't use ordering unless both a 'field' and 'direction' are provided
			useOrdering := orderBy != "" && direction != ""
			if useOrdering {
				vars["orderByField"] = githubv4.DiscussionOrderField(orderBy)
				vars["orderByDirection"] = githubv4.OrderDirection(direction)
			}

			if categoryID != nil {
				vars["categoryId"] = *categoryID
			}

			discussionQuery := getQueryType(useOrdering, categoryID)
			if err := client.Query(ctx, discussionQuery, vars); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			// Extract and convert all discussion nodes using the common interface
			var discussions []*github.Discussion
			var pageInfo PageInfoFragment
			var totalCount githubv4.Int
			if queryResult, ok := discussionQuery.(DiscussionQueryResult); ok {
				fragment := queryResult.GetDiscussionFragment()
				for _, node := range fragment.Nodes {
					discussions = append(discussions, fragmentToDiscussion(node))
				}
				pageInfo = fragment.PageInfo
				totalCount = fragment.TotalCount
			}

			// Create response with pagination info
			response := map[string]interface{}{
				"discussions": discussions,
				"pageInfo": map[string]interface{}{
					"hasNextPage":     pageInfo.HasNextPage,
					"hasPreviousPage": pageInfo.HasPreviousPage,
					"startCursor":     string(pageInfo.StartCursor),
					"endCursor":       string(pageInfo.EndCursor),
				},
				"totalCount": totalCount,
			}

			out, err := json.Marshal(response)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal discussions: %w", err)
			}
			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func GetDiscussion(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "get_discussion",
			Description: t("TOOL_GET_DISCUSSION_DESCRIPTION", "Get a specific discussion by ID"),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_GET_DISCUSSION_USER_TITLE", "Get discussion"),
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name",
					},
					"discussionNumber": {
						Type:        "number",
						Description: "Discussion Number",
					},
				},
				Required: []string{"owner", "repo", "discussionNumber"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			// Decode params
			var params struct {
				Owner            string
				Repo             string
				DiscussionNumber int32
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			var q struct {
				Repository struct {
					Discussion struct {
						Number         githubv4.Int
						Title          githubv4.String
						Body           githubv4.String
						CreatedAt      githubv4.DateTime
						Closed         githubv4.Boolean
						IsAnswered     githubv4.Boolean
						AnswerChosenAt *githubv4.DateTime
						URL            githubv4.String `graphql:"url"`
						Category       struct {
							Name githubv4.String
						} `graphql:"category"`
					} `graphql:"discussion(number: $discussionNumber)"`
				} `graphql:"repository(owner: $owner, name: $repo)"`
			}
			vars := map[string]interface{}{
				"owner":            githubv4.String(params.Owner),
				"repo":             githubv4.String(params.Repo),
				"discussionNumber": githubv4.Int(params.DiscussionNumber),
			}
			if err := client.Query(ctx, &q, vars); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			d := q.Repository.Discussion

			// Build response as map to include fields not present in go-github's Discussion struct.
			// The go-github library's Discussion type lacks isAnswered and answerChosenAt fields,
			// so we use map[string]interface{} for the response (consistent with other functions
			// like ListDiscussions and GetDiscussionComments).
			response := map[string]interface{}{
				"number":     int(d.Number),
				"title":      string(d.Title),
				"body":       string(d.Body),
				"url":        string(d.URL),
				"closed":     bool(d.Closed),
				"isAnswered": bool(d.IsAnswered),
				"createdAt":  d.CreatedAt.Time,
				"category": map[string]interface{}{
					"name": string(d.Category.Name),
				},
			}

			// Add optional timestamp fields if present
			if d.AnswerChosenAt != nil {
				response["answerChosenAt"] = d.AnswerChosenAt.Time
			}

			out, err := json.Marshal(response)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal discussion: %w", err)
			}

			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func GetDiscussionComments(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "get_discussion_comments",
			Description: t("TOOL_GET_DISCUSSION_COMMENTS_DESCRIPTION", "Get comments from a discussion"),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_GET_DISCUSSION_COMMENTS_USER_TITLE", "Get discussion comments"),
				ReadOnlyHint: true,
			},
			InputSchema: WithCursorPagination(&jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name",
					},
					"discussionNumber": {
						Type:        "number",
						Description: "Discussion Number",
					},
				},
				Required: []string{"owner", "repo", "discussionNumber"},
			}),
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			// Decode params
			var params struct {
				Owner            string
				Repo             string
				DiscussionNumber int32
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			// Get pagination parameters and convert to GraphQL format
			pagination, err := OptionalCursorPaginationParams(args)
			if err != nil {
				return nil, nil, err
			}

			// Check if pagination parameters were explicitly provided
			_, perPageProvided := args["perPage"]
			paginationExplicit := perPageProvided

			paginationParams, err := pagination.ToGraphQLParams()
			if err != nil {
				return nil, nil, err
			}

			// Use default of 30 if pagination was not explicitly provided
			if !paginationExplicit {
				defaultFirst := int32(DefaultGraphQLPageSize)
				paginationParams.First = &defaultFirst
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			var q struct {
				Repository struct {
					Discussion struct {
						Comments struct {
							Nodes []struct {
								ID   githubv4.ID
								Body githubv4.String
								URL  githubv4.String `graphql:"url"`
							}
							PageInfo struct {
								HasNextPage     githubv4.Boolean
								HasPreviousPage githubv4.Boolean
								StartCursor     githubv4.String
								EndCursor       githubv4.String
							}
							TotalCount int
						} `graphql:"comments(first: $first, after: $after)"`
					} `graphql:"discussion(number: $discussionNumber)"`
				} `graphql:"repository(owner: $owner, name: $repo)"`
			}
			vars := map[string]interface{}{
				"owner":            githubv4.String(params.Owner),
				"repo":             githubv4.String(params.Repo),
				"discussionNumber": githubv4.Int(params.DiscussionNumber),
				"first":            githubv4.Int(*paginationParams.First),
			}
			if paginationParams.After != nil {
				vars["after"] = githubv4.String(*paginationParams.After)
			} else {
				vars["after"] = (*githubv4.String)(nil)
			}
			if err := client.Query(ctx, &q, vars); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			var comments []map[string]any
			for _, c := range q.Repository.Discussion.Comments.Nodes {
				comments = append(comments, map[string]any{
					"id":   fmt.Sprint(c.ID),
					"body": string(c.Body),
					"url":  string(c.URL),
				})
			}

			// Create response with pagination info
			response := map[string]interface{}{
				"comments": comments,
				"pageInfo": map[string]interface{}{
					"hasNextPage":     q.Repository.Discussion.Comments.PageInfo.HasNextPage,
					"hasPreviousPage": q.Repository.Discussion.Comments.PageInfo.HasPreviousPage,
					"startCursor":     string(q.Repository.Discussion.Comments.PageInfo.StartCursor),
					"endCursor":       string(q.Repository.Discussion.Comments.PageInfo.EndCursor),
				},
				"totalCount": q.Repository.Discussion.Comments.TotalCount,
			}

			out, err := json.Marshal(response)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal comments: %w", err)
			}

			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func ListDiscussionCategories(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "list_discussion_categories",
			Description: t("TOOL_LIST_DISCUSSION_CATEGORIES_DESCRIPTION", "List discussion categories with their id and name, for a repository or organisation."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_LIST_DISCUSSION_CATEGORIES_USER_TITLE", "List discussion categories"),
				ReadOnlyHint: true,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name. If not provided, discussion categories will be queried at the organisation level.",
					},
				},
				Required: []string{"owner"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			owner, err := RequiredParam[string](args, "owner")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			repo, err := OptionalParam[string](args, "repo")
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			// when not provided, default to the .github repository
			// this will query discussion categories at the organisation level
			if repo == "" {
				repo = ".github"
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			var q struct {
				Repository struct {
					DiscussionCategories struct {
						Nodes []struct {
							ID   githubv4.ID
							Name githubv4.String
						}
						PageInfo struct {
							HasNextPage     githubv4.Boolean
							HasPreviousPage githubv4.Boolean
							StartCursor     githubv4.String
							EndCursor       githubv4.String
						}
						TotalCount int
					} `graphql:"discussionCategories(first: $first)"`
				} `graphql:"repository(owner: $owner, name: $repo)"`
			}
			vars := map[string]interface{}{
				"owner": githubv4.String(owner),
				"repo":  githubv4.String(repo),
				"first": githubv4.Int(25),
			}
			if err := client.Query(ctx, &q, vars); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			var categories []map[string]string
			for _, c := range q.Repository.DiscussionCategories.Nodes {
				categories = append(categories, map[string]string{
					"id":   fmt.Sprint(c.ID),
					"name": string(c.Name),
				})
			}

			// Create response with pagination info
			response := map[string]interface{}{
				"categories": categories,
				"pageInfo": map[string]interface{}{
					"hasNextPage":     q.Repository.DiscussionCategories.PageInfo.HasNextPage,
					"hasPreviousPage": q.Repository.DiscussionCategories.PageInfo.HasPreviousPage,
					"startCursor":     string(q.Repository.DiscussionCategories.PageInfo.StartCursor),
					"endCursor":       string(q.Repository.DiscussionCategories.PageInfo.EndCursor),
				},
				"totalCount": q.Repository.DiscussionCategories.TotalCount,
			}

			out, err := json.Marshal(response)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal discussion categories: %w", err)
			}
			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func CreateDiscussion(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "create_discussion",
			Description: t("TOOL_CREATE_DISCUSSION_DESCRIPTION", "Create a new discussion in a repository."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_CREATE_DISCUSSION_USER_TITLE", "Create discussion"),
				ReadOnlyHint: false,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name",
					},
					"title": {
						Type:        "string",
						Description: "Discussion title",
					},
					"body": {
						Type:        "string",
						Description: "Discussion body (Markdown)",
					},
					"category_id": {
						Type:        "string",
						Description: "Discussion category node ID. If provided, this is used directly.",
					},
					"category_name": {
						Type:        "string",
						Description: "Discussion category name. If provided, it will be resolved to a category ID.",
					},
				},
				Required: []string{"owner", "repo", "title", "body"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			var params struct {
				Owner        string
				Repo         string
				Title        string
				Body         string
				CategoryID   string `mapstructure:"category_id"`
				CategoryName string `mapstructure:"category_name"`
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			repoID, err := getRepositoryID(ctx, client, params.Owner, params.Repo)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			categoryID, err := resolveDiscussionCategoryID(ctx, client, params.Owner, params.Repo, params.CategoryID, params.CategoryName)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			var mutation struct {
				CreateDiscussion struct {
					Discussion struct {
						ID     githubv4.ID
						Number githubv4.Int
						URL    githubv4.String `graphql:"url"`
					}
				} `graphql:"createDiscussion(input: $input)"`
			}

			if err := client.Mutate(ctx, &mutation, githubv4.CreateDiscussionInput{
				RepositoryID: repoID,
				Title:        githubv4.String(params.Title),
				Body:         githubv4.String(params.Body),
				CategoryID:   *categoryID,
			}, nil); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			out, err := json.Marshal(map[string]any{
				"id":     fmt.Sprint(mutation.CreateDiscussion.Discussion.ID),
				"number": int(mutation.CreateDiscussion.Discussion.Number),
				"url":    string(mutation.CreateDiscussion.Discussion.URL),
			})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal create discussion response: %w", err)
			}
			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func UpdateDiscussion(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "update_discussion",
			Description: t("TOOL_UPDATE_DISCUSSION_DESCRIPTION", "Update a discussion (title/body/category) in a repository."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_UPDATE_DISCUSSION_USER_TITLE", "Update discussion"),
				ReadOnlyHint: false,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name",
					},
					"discussionNumber": {
						Type:        "number",
						Description: "Discussion Number",
					},
					"title": {
						Type:        "string",
						Description: "New discussion title (optional)",
					},
					"body": {
						Type:        "string",
						Description: "New discussion body (optional)",
					},
					"category_id": {
						Type:        "string",
						Description: "New discussion category node ID (optional). If provided, this is used directly.",
					},
					"category_name": {
						Type:        "string",
						Description: "New discussion category name (optional). If provided, it will be resolved to a category ID.",
					},
				},
				Required: []string{"owner", "repo", "discussionNumber"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			var params struct {
				Owner            string
				Repo             string
				DiscussionNumber int32
				Title            string
				Body             string
				CategoryID       string `mapstructure:"category_id"`
				CategoryName     string `mapstructure:"category_name"`
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			if params.Title == "" && params.Body == "" && params.CategoryID == "" && params.CategoryName == "" {
				return utils.NewToolResultError("at least one of title, body, category_id, or category_name must be provided"), nil, nil
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			discussionID, err := getDiscussionID(ctx, client, params.Owner, params.Repo, params.DiscussionNumber)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			var title *githubv4.String
			if params.Title != "" {
				s := githubv4.String(params.Title)
				title = &s
			}
			var body *githubv4.String
			if params.Body != "" {
				s := githubv4.String(params.Body)
				body = &s
			}

			var categoryID *githubv4.ID
			if params.CategoryID != "" || params.CategoryName != "" {
				resolved, err := resolveDiscussionCategoryID(ctx, client, params.Owner, params.Repo, params.CategoryID, params.CategoryName)
				if err != nil {
					return utils.NewToolResultError(err.Error()), nil, nil
				}
				categoryID = resolved
			}

			var mutation struct {
				UpdateDiscussion struct {
					Discussion struct {
						ID     githubv4.ID
						Number githubv4.Int
						URL    githubv4.String `graphql:"url"`
					}
				} `graphql:"updateDiscussion(input: $input)"`
			}

			input := githubv4.UpdateDiscussionInput{
				DiscussionID: discussionID,
				Title:        title,
				Body:         body,
				CategoryID:   categoryID,
			}

			if err := client.Mutate(ctx, &mutation, input, nil); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			out, err := json.Marshal(map[string]any{
				"id":     fmt.Sprint(mutation.UpdateDiscussion.Discussion.ID),
				"number": int(mutation.UpdateDiscussion.Discussion.Number),
				"url":    string(mutation.UpdateDiscussion.Discussion.URL),
			})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal update discussion response: %w", err)
			}
			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func AddDiscussionComment(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "add_discussion_comment",
			Description: t("TOOL_ADD_DISCUSSION_COMMENT_DESCRIPTION", "Add a comment to a discussion."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_ADD_DISCUSSION_COMMENT_USER_TITLE", "Add discussion comment"),
				ReadOnlyHint: false,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"owner": {
						Type:        "string",
						Description: "Repository owner",
					},
					"repo": {
						Type:        "string",
						Description: "Repository name",
					},
					"discussionNumber": {
						Type:        "number",
						Description: "Discussion Number",
					},
					"body": {
						Type:        "string",
						Description: "Comment body (Markdown)",
					},
					"reply_to_id": {
						Type:        "string",
						Description: "Optional discussion comment node ID to reply to.",
					},
				},
				Required: []string{"owner", "repo", "discussionNumber", "body"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			var params struct {
				Owner            string
				Repo             string
				DiscussionNumber int32
				Body             string
				ReplyToID        string `mapstructure:"reply_to_id"`
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			discussionID, err := getDiscussionID(ctx, client, params.Owner, params.Repo, params.DiscussionNumber)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			var replyToID *githubv4.ID
			if params.ReplyToID != "" {
				id := githubv4.ID(params.ReplyToID)
				replyToID = &id
			}

			var mutation struct {
				AddDiscussionComment struct {
					Comment struct {
						ID  githubv4.ID
						URL githubv4.String `graphql:"url"`
					}
				} `graphql:"addDiscussionComment(input: $input)"`
			}

			if err := client.Mutate(ctx, &mutation, githubv4.AddDiscussionCommentInput{
				DiscussionID: discussionID,
				Body:         githubv4.String(params.Body),
				ReplyToID:    replyToID,
			}, nil); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			out, err := json.Marshal(map[string]any{
				"id":  fmt.Sprint(mutation.AddDiscussionComment.Comment.ID),
				"url": string(mutation.AddDiscussionComment.Comment.URL),
			})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal add discussion comment response: %w", err)
			}
			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func UpdateDiscussionComment(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "update_discussion_comment",
			Description: t("TOOL_UPDATE_DISCUSSION_COMMENT_DESCRIPTION", "Update an existing discussion comment."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_UPDATE_DISCUSSION_COMMENT_USER_TITLE", "Update discussion comment"),
				ReadOnlyHint: false,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"comment_id": {
						Type:        "string",
						Description: "Discussion comment node ID",
					},
					"body": {
						Type:        "string",
						Description: "New comment body (Markdown)",
					},
				},
				Required: []string{"comment_id", "body"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			var params struct {
				CommentID string `mapstructure:"comment_id"`
				Body      string
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			var mutation struct {
				UpdateDiscussionComment struct {
					Comment struct {
						ID  githubv4.ID
						URL githubv4.String `graphql:"url"`
					}
				} `graphql:"updateDiscussionComment(input: $input)"`
			}

			if err := client.Mutate(ctx, &mutation, githubv4.UpdateDiscussionCommentInput{
				CommentID: githubv4.ID(params.CommentID),
				Body:      githubv4.String(params.Body),
			}, nil); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			out, err := json.Marshal(map[string]any{
				"id":  fmt.Sprint(mutation.UpdateDiscussionComment.Comment.ID),
				"url": string(mutation.UpdateDiscussionComment.Comment.URL),
			})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal update discussion comment response: %w", err)
			}
			return utils.NewToolResultText(string(out)), nil, nil
		},
	)
}

func DeleteDiscussionComment(t translations.TranslationHelperFunc) inventory.ServerTool {
	return NewTool(
		ToolsetMetadataDiscussions,
		mcp.Tool{
			Name:        "delete_discussion_comment",
			Description: t("TOOL_DELETE_DISCUSSION_COMMENT_DESCRIPTION", "Delete an existing discussion comment."),
			Annotations: &mcp.ToolAnnotations{
				Title:        t("TOOL_DELETE_DISCUSSION_COMMENT_USER_TITLE", "Delete discussion comment"),
				ReadOnlyHint: false,
			},
			InputSchema: &jsonschema.Schema{
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"comment_id": {
						Type:        "string",
						Description: "Discussion comment node ID",
					},
				},
				Required: []string{"comment_id"},
			},
		},
		func(ctx context.Context, deps ToolDependencies, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			var params struct {
				CommentID string `mapstructure:"comment_id"`
			}
			if err := mapstructure.Decode(args, &params); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			client, err := deps.GetGQLClient(ctx)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("failed to get GitHub GQL client: %v", err)), nil, nil
			}

			var mutation struct {
				DeleteDiscussionComment struct {
					ClientMutationID githubv4.String
				} `graphql:"deleteDiscussionComment(input: $input)"`
			}

			if err := client.Mutate(ctx, &mutation, githubv4.DeleteDiscussionCommentInput{
				ID: githubv4.ID(params.CommentID),
			}, nil); err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			return utils.NewToolResultText("discussion comment deleted successfully"), nil, nil
		},
	)
}

func getDiscussionID(ctx context.Context, client *githubv4.Client, owner string, repo string, discussionNumber int32) (githubv4.ID, error) {
	var q struct {
		Repository struct {
			Discussion struct {
				ID githubv4.ID
			} `graphql:"discussion(number: $discussionNumber)"`
		} `graphql:"repository(owner: $owner, name: $repo)"`
	}

	vars := map[string]any{
		"owner":            githubv4.String(owner),
		"repo":             githubv4.String(repo),
		"discussionNumber": githubv4.Int(discussionNumber),
	}
	if err := client.Query(ctx, &q, vars); err != nil {
		return "", fmt.Errorf("failed to get discussion ID: %w", err)
	}
	return q.Repository.Discussion.ID, nil
}

func resolveDiscussionCategoryID(ctx context.Context, client *githubv4.Client, owner string, repo string, categoryID string, categoryName string) (*githubv4.ID, error) {
	if categoryID != "" {
		id := githubv4.ID(categoryID)
		return &id, nil
	}
	if categoryName == "" {
		return nil, fmt.Errorf("either category_id or category_name is required")
	}

	var q struct {
		Repository struct {
			DiscussionCategories struct {
				Nodes []struct {
					ID   githubv4.ID
					Name githubv4.String
				}
			} `graphql:"discussionCategories(first: $first)"`
		} `graphql:"repository(owner: $owner, name: $repo)"`
	}
	vars := map[string]any{
		"owner": githubv4.String(owner),
		"repo":  githubv4.String(repo),
		"first": githubv4.Int(100),
	}
	if err := client.Query(ctx, &q, vars); err != nil {
		return nil, fmt.Errorf("failed to list discussion categories: %w", err)
	}

	for _, c := range q.Repository.DiscussionCategories.Nodes {
		if strings.EqualFold(string(c.Name), categoryName) {
			id := c.ID
			return &id, nil
		}
	}

	return nil, fmt.Errorf("discussion category %q not found; use list_discussion_categories to see available categories", categoryName)
}
