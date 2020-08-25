// Copyright 2020 New Relic Corporation. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dm

import (
	"fmt"
	"github.com/newrelic/infrastructure-agent/pkg/backend/identityapi"
	"github.com/newrelic/infrastructure-agent/pkg/entity"
	"github.com/newrelic/infrastructure-agent/pkg/integrations/v4/protocol"
)

type registeredEntitiesNameToID map[string]entity.ID
type unregisteredEntitiesNamed map[string]unregisteredEntity
type reason string

type unregisteredEntity struct {
	Reason reason
	Err    error
	Entity protocol.Entity
}

type UnregisteredEntities []unregisteredEntity

const reasonClientError = "Identity client error"
const reasonEntityError = "Entity error"

func newUnregisteredEntity(entity protocol.Entity, reason reason, err error) unregisteredEntity {
	return unregisteredEntity{
		Entity: entity,
		Reason: reason,
		Err:    err,
	}
}

type idProvider interface {
	ResolveEntities(agentIdn entity.Identity, entities []protocol.Entity) (registeredEntities registeredEntitiesNameToID, unregisteredEntities UnregisteredEntities)
}

type cachedIdProvider struct {
	client               identityapi.RegisterClient
	cache                registeredEntitiesNameToID
	unregisteredEntities unregisteredEntitiesNamed
}

func NewCachedIDProvider(client identityapi.RegisterClient) *cachedIdProvider {
	cache := make(registeredEntitiesNameToID)
	unregisteredEntities := make(unregisteredEntitiesNamed)
	return &cachedIdProvider{
		client:               client,
		cache:                cache,
		unregisteredEntities: unregisteredEntities,
	}
}

func (p *cachedIdProvider) ResolveEntities(agentIdn entity.Identity, entities []protocol.Entity) (registeredEntities registeredEntitiesNameToID, unregisteredEntities UnregisteredEntities) {
	unregisteredEntities = make(UnregisteredEntities, 0)
	registeredEntities = make(registeredEntitiesNameToID, 0)
	entitiesToRegister := make([]protocol.Entity, 0)

	for _, e := range entities {
		if foundID, ok := p.cache[e.Name]; ok {
			registeredEntities[e.Name] = foundID
		} else {
			entitiesToRegister = append(entitiesToRegister, e)
		}
	}

	if len(entitiesToRegister) == 0 {
		return registeredEntities, unregisteredEntities
	}

	response, _, errClient := p.client.RegisterBatchEntities(
		agentIdn.ID,
		entitiesToRegister)

	type nameToEntityType map[string]protocol.Entity
	nameToEntity := make(nameToEntityType, len(entitiesToRegister))

	for i := range entitiesToRegister {
		nameToEntity[entitiesToRegister[i].Name] = entitiesToRegister[i]
	}

	if errClient != nil {
		for i := range entitiesToRegister {
			unregisteredEntity := newUnregisteredEntity(entitiesToRegister[i], reasonClientError, errClient)
			p.unregisteredEntities[entitiesToRegister[i].Name] = unregisteredEntity
			unregisteredEntities = append(unregisteredEntities, unregisteredEntity)
		}
	} else {
		for i := range response {
			if response[i].Err != "" {
				unregisteredEntity := newUnregisteredEntity(nameToEntity[response[i].Name], reasonEntityError, fmt.Errorf(response[i].Err))
				p.unregisteredEntities[response[i].Name] = unregisteredEntity
				unregisteredEntities = append(unregisteredEntities, unregisteredEntity)
				continue
			}

			p.cache[string(response[i].Key)] = response[i].ID
			registeredEntities[string(response[i].Key)] = response[i].ID
		}
	}

	return registeredEntities, unregisteredEntities
}
