package fga

import (
	"context"
	"fmt"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/platform-mesh/golang-commons/fga/util"
	"github.com/platform-mesh/golang-commons/logger"

	"github.com/platform-mesh/search/internal/service/search"
)

const batchCheckChunkSize = 100

type Authorizer struct {
	client openfgav1.OpenFGAServiceClient
}

func NewAuthorizer(client openfgav1.OpenFGAServiceClient) *Authorizer {
	return &Authorizer{client: client}
}

func (a *Authorizer) FilterAuthorized(ctx context.Context, req search.AuthorizationRequest) (search.AuthorizationResult, error) {
	log := logger.LoadLoggerFromContext(ctx)
	allowed := make([]bool, len(req.Hits))
	if len(req.Hits) == 0 {
		return search.AuthorizationResult{Allowed: allowed}, nil
	}

	storeID, err := a.resolveStoreID(ctx, req.Organization)
	if err != nil {
		log.Error().
			Err(err).
			Str("organization", req.Organization).
			Str("user", req.User).
			Int("hits", len(req.Hits)).
			Msg("failed to resolve OpenFGA store ID")
		return search.AuthorizationResult{}, fmt.Errorf("resolve store ID: %w", err)
	}

	result := search.AuthorizationResult{Allowed: allowed}

	for _, chunk := range chunkRanges(len(req.Hits), batchCheckChunkSize) {
		start := chunk[0]
		end := chunk[1]
		items := make([]*openfgav1.BatchCheckItem, 0, end-start)
		indicesByCorrelation := make(map[string]int, end-start)

		for idx := start; idx < end; idx++ {
			item, missingContext := buildBatchCheckItem(req.User, req.Relation, idx, req.Hits[idx])
			if missingContext {
				result.DroppedMissingContext++
				continue
			}
			items = append(items, item)
			indicesByCorrelation[item.CorrelationId] = idx
		}

		if len(items) == 0 {
			continue
		}

		batchResponse, err := a.client.BatchCheck(ctx, &openfgav1.BatchCheckRequest{
			StoreId: storeID,
			Checks:  items,
		})
		if err != nil {
			log.Error().
				Err(err).
				Str("organization", req.Organization).
				Str("storeID", storeID).
				Str("user", req.User).
				Int("checks", len(items)).
				Msg("OpenFGA BatchCheck failed")
			return search.AuthorizationResult{}, fmt.Errorf("openfga batch check: %w", err)
		}
		result.Calls++

		for correlationID, index := range indicesByCorrelation {
			entry, ok := batchResponse.GetResult()[correlationID]
			if !ok {
				result.Denied++
				continue
			}
			if entry.GetAllowed() {
				result.Allowed[index] = true
				continue
			}
			result.Denied++
		}
	}

	return result, nil
}

func (a *Authorizer) resolveStoreID(ctx context.Context, org string) (string, error) {
	res, err := a.client.ListStores(ctx, &openfgav1.ListStoresRequest{})
	if err != nil {
		return "", fmt.Errorf("list stores: %w", err)
	}
	for _, store := range res.GetStores() {
		if strings.TrimSpace(store.GetName()) == org {
			return store.GetId(), nil
		}
	}
	return "", fmt.Errorf("no OpenFGA store found for organization %q", org)
}

func chunkRanges(total, chunkSize int) [][2]int {
	if total <= 0 || chunkSize <= 0 {
		return nil
	}

	ranges := make([][2]int, 0, (total+chunkSize-1)/chunkSize)
	for start := 0; start < total; start += chunkSize {
		end := start + chunkSize
		if end > total {
			end = total
		}
		ranges = append(ranges, [2]int{start, end})
	}

	return ranges
}

func buildBatchCheckItem(user, relation string, index int, hit search.OpenSearchHit) (*openfgav1.BatchCheckItem, bool) {
	ctx, ok := buildAuthorizationContext(hit.Source)
	if !ok {
		return nil, true
	}

	tupleKey := &openfgav1.CheckRequestTupleKey{
		User:     fmt.Sprintf("user:%s", user),
		Relation: relation,
		Object:   ctx.object,
	}

	return &openfgav1.BatchCheckItem{
		TupleKey:         tupleKey,
		ContextualTuples: &openfgav1.ContextualTupleKeys{TupleKeys: ctx.contextualTuples},
		CorrelationId:    fmt.Sprintf("%d", index),
	}, false
}

type authzContext struct {
	object           string
	contextualTuples []*openfgav1.TupleKey
}

func buildAuthorizationContext(source map[string]interface{}) (authzContext, bool) {
	if source == nil {
		return authzContext{}, false
	}

	kind := readString(source, "kind")
	name := readString(source, "name")
	namespace := readString(source, "namespace")
	apiGroup := readString(source, "api_group")
	clusterName := readString(source, "cluster_name")
	organizationID := readString(source, "organization_id")
	accountID := readString(source, "account_id")
	accountName := readString(source, "account_name")

	resourceClusterID := clusterName
	if resourceClusterID == "" {
		resourceClusterID = firstNonEmpty(accountID, organizationID)
	}

	if kind == "" || name == "" || resourceClusterID == "" {
		return authzContext{}, false
	}

	if namespace != "" {
		if accountName == "" {
			return authzContext{}, false
		}
	}

	resourceType := util.ConvertToTypeName(apiGroup, kind)
	object := fmt.Sprintf("%s:%s/%s", resourceType, resourceClusterID, name)
	if namespace != "" {
		object = fmt.Sprintf("%s:%s/%s/%s", resourceType, resourceClusterID, namespace, name)
	}

	accountClusterID := firstNonEmpty(accountID, organizationID, resourceClusterID)
	accountObject := ""
	if accountName != "" && accountClusterID != "" {
		accountType := util.ConvertToTypeName("core.platform-mesh.io", "Account")
		accountObject = fmt.Sprintf("%s:%s/%s", accountType, accountClusterID, accountName)
	}

	tuples := make([]*openfgav1.TupleKey, 0, 2)
	resourceManaged := managedTuple(apiGroup, kind)

	if namespace != "" && accountObject != "" {
		namespaceType := util.ConvertToTypeName("", "Namespace")
		namespaceObject := fmt.Sprintf("%s:%s/%s", namespaceType, resourceClusterID, namespace)

		tuples = append(tuples, &openfgav1.TupleKey{
			Object:   namespaceObject,
			Relation: "parent",
			User:     accountObject,
		})

		if !resourceManaged {
			tuples = append(tuples, &openfgav1.TupleKey{
				Object:   object,
				Relation: "parent",
				User:     namespaceObject,
			})
		}
	} else if accountObject != "" && !resourceManaged {
		tuples = append(tuples, &openfgav1.TupleKey{
			Object:   object,
			Relation: "parent",
			User:     accountObject,
		})
	}

	return authzContext{object: object, contextualTuples: tuples}, true
}

func managedTuple(group, kind string) bool {
	if strings.EqualFold(group, "core.platform-mesh.io") && strings.EqualFold(kind, "Account") {
		return true
	}
	return false
}

func readString(source map[string]interface{}, key string) string {
	v, ok := source[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
