// Copyright 2020 New Relic Corporation. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package dm

import (
	"fmt"
	"github.com/newrelic/infrastructure-agent/pkg/backend/identityapi"
	"github.com/newrelic/infrastructure-agent/pkg/entity"
	"github.com/newrelic/infrastructure-agent/pkg/integrations/v4/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"sync"
	"testing"
	"time"
)

type mockedRegisterClient struct {
	mock.Mock
}

func (mk *mockedRegisterClient) RegisterBatchEntities(agentEntityID entity.ID, entities []protocol.Entity,
) ([]identityapi.RegisterEntityResponse, time.Duration, error) {

	args := mk.Called(agentEntityID, entities)
	return args.Get(0).([]identityapi.RegisterEntityResponse),
		args.Get(1).(time.Duration),
		args.Error(2)
}

func (mk *mockedRegisterClient) RegisterEntity(agentEntityID entity.ID, entity protocol.Entity) (identityapi.RegisterEntityResponse, error) {
	return identityapi.RegisterEntityResponse{}, nil
}

func (mk *mockedRegisterClient) RegisterEntitiesRemoveMe(agentEntityID entity.ID, entities []identityapi.RegisterEntity) ([]identityapi.RegisterEntityResponse, time.Duration, error) {
	return nil, time.Second, nil
}

func TestIdProvider_Entities_MemoryFirst(t *testing.T) {

	agentIdentity := func() entity.Identity{
		return entity.Identity{ID: 13}
	}

	registerClient := &mockedRegisterClient{}
	registerClient.
		On("RegisterBatchEntities", agentIdentity().ID, mock.Anything).
		Return([]identityapi.RegisterEntityResponse{}, time.Second, nil)

	cache := registeredEntitiesNameToID{
		"remote_entity_flex":  6543,
		"remote_entity_nginx": 1234,
	}

	entities := []protocol.Entity{
		{Name: "remote_entity_flex"},
		{Name: "remote_entity_nginx"},
	}

	idProvider := NewCachedIDProvider(registerClient, agentIdentity)

	idProvider.cache = cache
	idProvider.ResolveEntities(entities)

	registerClient.AssertNotCalled(t, "RegisterBatchEntities")
}

func TestIdProvider_Entities_OneCachedAnotherRegistered(t *testing.T) {

	agentIdentity := func() entity.Identity{
		return entity.Identity{ID: 13}
	}
	entitiesForRegisterClient := []protocol.Entity{
		{
			Name: "remote_entity_nginx",
		},
	}

	registerClientResponse := []identityapi.RegisterEntityResponse{
		{
			ID:   1234,
			Key:  "remote_entity_nginx",
			Name: "remote_entity_nginx",
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	registerClient := &mockedRegisterClient{}
	registerClient.
		On("RegisterBatchEntities", mock.Anything, mock.Anything).
		Return(registerClientResponse, time.Second, nil).Run(func(args mock.Arguments){
			wg.Done()
	})

	cache := registeredEntitiesNameToID{
		"remote_entity_flex": 6543,
	}

	entities := []protocol.Entity{
		{Name: "remote_entity_flex"},
		{Name: "remote_entity_nginx"},
	}

	idProvider := NewCachedIDProvider(registerClient, agentIdentity)

	// change suggested - dont test internals -> make extra call to fill the cache
	idProvider.cache = cache
	registeredEntities, unregisteredEntities := idProvider.ResolveEntities(entities)

	assert.Len(t, registeredEntities, 1)
	assert.Len(t, unregisteredEntities, 1)
	// do first request check stuff empty
	wg.Wait()

	time.Sleep(time.Second * 1)

	registeredEntities, unregisteredEntities = idProvider.ResolveEntities(entities)
	assert.Len(t, registeredEntities, 2)
	assert.Len(t, unregisteredEntities, 0)

	registerClient.AssertCalled(t, "RegisterBatchEntities", agentIdentity().ID, entitiesForRegisterClient)
}

func TestIdProvider_Entities_ErrorsHandling(t *testing.T) {

	testCases := []struct {
		name                         string
		agentIdn                     func() entity.Identity
		cache                        registeredEntitiesNameToID
		entitiesForRegisterClient    []protocol.Entity
		registerClientResponse       []identityapi.RegisterEntityResponse
		registerClientResponseErr    error
		entitiesToRegister           []protocol.Entity
		registeredEntitiesExpected   registeredEntitiesNameToID
		unregisteredEntitiesExpected UnregisteredEntities
	}{
		{
			name:     "OneCached_OneFailed_ErrClient",
			agentIdn: func() entity.Identity{
				return entity.Identity{ID: 13}
			},
			cache: registeredEntitiesNameToID{
				"remote_entity_flex": 6543,
			},
			entitiesForRegisterClient: []protocol.Entity{
				{
					Name: "remote_entity_nginx",
				},
			},
			registerClientResponse:    []identityapi.RegisterEntityResponse{},
			registerClientResponseErr: fmt.Errorf("internal server error"),
			entitiesToRegister: []protocol.Entity{
				{Name: "remote_entity_flex"},
				{Name: "remote_entity_nginx"},
			},
			registeredEntitiesExpected: registeredEntitiesNameToID{
				"remote_entity_flex": 6543,
			},
			unregisteredEntitiesExpected: UnregisteredEntities{
				{
					Reason: reasonClientError,
					Err:    fmt.Errorf("internal server error"),
					Entity: protocol.Entity{
						Name: "remote_entity_nginx",
					},
				},
			},
		},
		{
			name:     "OneCached_OneFailed_ErrEntity",
			agentIdn: func() entity.Identity{
				return entity.Identity{ID: 13}
			},
			cache: registeredEntitiesNameToID{
				"remote_entity_flex": 6543,
			},
			entitiesForRegisterClient: []protocol.Entity{
				{
					Name: "remote_entity_nginx",
				},
			},
			registerClientResponse: []identityapi.RegisterEntityResponse{
				{
					Key:  "remote_entity_nginx_Key",
					Name: "remote_entity_nginx",
					Err:  "invalid entityName",
				},
			},
			entitiesToRegister: []protocol.Entity{
				{Name: "remote_entity_flex"},
				{Name: "remote_entity_nginx"},
			},
			registeredEntitiesExpected: registeredEntitiesNameToID{
				"remote_entity_flex": 6543,
			},
			unregisteredEntitiesExpected: UnregisteredEntities{
				{
					Reason: reasonEntityError,
					Err:    fmt.Errorf("invalid entityName"),
					Entity: protocol.Entity{
						Name: "remote_entity_nginx",
					},
				},
			},
		},
		{
			name:     "OneCached_OneRegistered_OneFailed_ErrEntity",
			agentIdn: func() entity.Identity{
				return entity.Identity{ID: 13}
			},
			cache: registeredEntitiesNameToID{
				"remote_entity_flex": 6543,
			},
			entitiesForRegisterClient: []protocol.Entity{
				{
					Name: "remote_entity_nginx",
				},
				{
					Name: "remote_entity_kafka",
				},
			},
			registerClientResponse: []identityapi.RegisterEntityResponse{
				{
					Key:  "remote_entity_nginx",
					Name: "remote_entity_nginx",
					Err:  "invalid entityName",
				},
				{
					ID:   1234,
					Key:  "remote_entity_kafka",
					Name: "remote_entity_kafka",
				},
			},
			entitiesToRegister: []protocol.Entity{
				{Name: "remote_entity_flex"},
				{Name: "remote_entity_nginx"},
				{Name: "remote_entity_kafka"},
			},
			registeredEntitiesExpected: registeredEntitiesNameToID{
				"remote_entity_flex":  6543,
				"remote_entity_kafka": 1234,
			},
			unregisteredEntitiesExpected: UnregisteredEntities{
				{
					Reason: reasonEntityError,
					Err:    fmt.Errorf("invalid entityName"),
					Entity: protocol.Entity{
						Name: "remote_entity_nginx",
					},
				},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {

			var wg sync.WaitGroup
			wg.Add(1)

			registerClient := &mockedRegisterClient{}
			registerClient.
				On("RegisterBatchEntities", mock.Anything, mock.Anything).
				Return(testCase.registerClientResponse, time.Second, testCase.registerClientResponseErr).
				Run(func(args mock.Arguments) {
					wg.Done()
				})

			idProvider := NewCachedIDProvider(registerClient, testCase.agentIdn)

			idProvider.cache = testCase.cache
			idProvider.ResolveEntities(testCase.entitiesToRegister)

			wg.Wait()
			time.Sleep(time.Second)

			registeredEntities, unregisteredEntities := idProvider.ResolveEntities(testCase.entitiesToRegister)

			assert.Equal(t, testCase.registeredEntitiesExpected, registeredEntities)

			assert.ElementsMatch(t, testCase.unregisteredEntitiesExpected, unregisteredEntities)

			registerClient.AssertCalled(t, "RegisterBatchEntities", testCase.agentIdn().ID, testCase.entitiesForRegisterClient)
		})
	}
}
