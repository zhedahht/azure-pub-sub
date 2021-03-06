package pubsub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	servicebus "github.com/Azure/azure-service-bus-go"
	"github.com/keikumata/azure-pub-sub/internal/reflection"
	servicebusinternal "github.com/keikumata/azure-pub-sub/internal/servicebus"
)

// Publisher is a struct to contain service bus entities relevant to publishing to a topic
type Publisher struct {
	namespace              *servicebus.Namespace
	topic                  *servicebus.Topic
	headers                map[string]string
	topicManagementOptions []servicebus.TopicManagementOption
}

// PublisherManagementOption provides structure for configuring a new Publisher
type PublisherManagementOption func(p *Publisher) error

// PublishOption provides structure for configuring when starting to publish to a specified topic
type PublishOption func(msg *servicebus.Message) error

// PublisherWithConnectionString configures a publisher with the information provided in a Service Bus connection string
func PublisherWithConnectionString(connStr string) PublisherManagementOption {
	return func(p *Publisher) error {
		if connStr == "" {
			return errors.New("no Service Bus connection string provided")
		}
		ns, err := getNamespace(servicebus.NamespaceWithConnectionString(connStr))
		if err != nil {
			return err
		}
		p.namespace = ns
		return nil
	}
}

// Deprecated. use PublisherWithManagedIdentityClientID or PublisherWithManagedIdentityResourceID instead
func PublisherWithManagedIdentity(serviceBusNamespaceName, managedIdentityClientID string) PublisherManagementOption {
	return PublisherWithManagedIdentityClientID(serviceBusNamespaceName, managedIdentityClientID)
}

// PublisherWithManagedIdentityResourceID configures a publisher with the attached managed identity and the Service bus resource name
func PublisherWithManagedIdentityResourceID(serviceBusNamespaceName, managedIdentityResourceID string) PublisherManagementOption {
	return func(p *Publisher) error {
		if serviceBusNamespaceName == "" {
			return errors.New("no Service Bus namespace provided")
		}
		ns, err := getNamespace(servicebusinternal.NamespaceWithManagedIdentityResourceID(serviceBusNamespaceName, managedIdentityResourceID))
		if err != nil {
			return err
		}
		p.namespace = ns
		return nil
	}
}

// PublisherWithManagedIdentityClientID configures a publisher with the attached managed identity and the Service bus resource name
func PublisherWithManagedIdentityClientID(serviceBusNamespaceName, managedIdentityClientID string) PublisherManagementOption {
	return func(p *Publisher) error {
		if serviceBusNamespaceName == "" {
			return errors.New("no Service Bus namespace provided")
		}
		ns, err := getNamespace(servicebusinternal.NamespaceWithManagedIdentityClientID(serviceBusNamespaceName, managedIdentityClientID))
		if err != nil {
			return err
		}
		p.namespace = ns
		return nil
	}
}

// SetDefaultHeader adds a header to every message published using the value specified from the message body
func SetDefaultHeader(headerName, msgKey string) PublisherManagementOption {
	return func(p *Publisher) error {
		if p.headers == nil {
			p.headers = make(map[string]string)
		}
		p.headers[headerName] = msgKey
		return nil
	}
}

// SetDuplicateDetection guarantees that the topic will have exactly-once delivery over a user-defined span of time.
// Defaults to 30 seconds with a maximum of 7 days
func SetDuplicateDetection(window *time.Duration) PublisherManagementOption {
	return func(p *Publisher) error {
		p.topicManagementOptions = append(p.topicManagementOptions, servicebus.TopicWithDuplicateDetection(window))
		return nil
	}
}

// SetMessageDelay schedules a message in the future
func SetMessageDelay(delay time.Duration) PublishOption {
	return func(msg *servicebus.Message) error {
		if msg == nil {
			return errors.New("message is nil. cannot assign message delay")
		}
		msg.ScheduleAt(time.Now().Add(delay))
		return nil
	}
}

// SetMessageID sets the messageID of the message. Used for duplication detection
func SetMessageID(messageID string) PublishOption {
	return func(msg *servicebus.Message) error {
		if msg == nil {
			return errors.New("message is nil. cannot assign message ID")
		}
		msg.ID = messageID
		return nil
	}
}

// SetCorrelationID sets the SetCorrelationID of the message.
func SetCorrelationID(correlationID string) PublishOption {
	return func(msg *servicebus.Message) error {
		if msg == nil {
			return errors.New("message is nil. cannot assign correlation ID")
		}
		msg.CorrelationID = correlationID
		return nil
	}
}

// NewPublisher creates a new service bus publisher
func NewPublisher(topicName string, opts ...PublisherManagementOption) (*Publisher, error) {
	ns, err := servicebus.NewNamespace()
	if err != nil {
		return nil, err
	}
	publisher := &Publisher{namespace: ns}
	for _, opt := range opts {
		err := opt(publisher)
		if err != nil {
			return nil, err
		}
	}
	topicEntity, err := ensureTopic(context.Background(), topicName, publisher.namespace, publisher.topicManagementOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to get topic: %w", err)
	}
	topic, err := publisher.namespace.NewTopic(topicEntity.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create new topic %s: %w", topicEntity.Name, err)
	}

	publisher.topic = topic
	return publisher, nil
}

// Publish publishes to the pre-configured Service Bus topic
func (p *Publisher) Publish(ctx context.Context, msg interface{}, opts ...PublishOption) error {
	msgJSON, err := json.Marshal(msg)

	// adding in user properties to enable filtering on listener side
	sbMsg := servicebus.NewMessageFromString(string(msgJSON))
	sbMsg.UserProperties = make(map[string]interface{})
	sbMsg.UserProperties["type"] = reflection.GetType(msg)

	// add in custom headers setup at initialization time
	for headerName, headerKey := range p.headers {
		val := reflection.GetReflectionValue(msg, headerKey)
		if val != nil {
			sbMsg.UserProperties[headerName] = val
		}
	}

	// now apply publishing options
	for _, opt := range opts {
		err := opt(sbMsg)
		if err != nil {
			return err
		}
	}

	// finally, send
	err = p.topic.Send(ctx, sbMsg)
	if err != nil {
		return fmt.Errorf("failed to send message to topic %s: %w", p.topic.Name, err)
	}
	return nil
}

func ensureTopic(ctx context.Context, name string, namespace *servicebus.Namespace, opts ...servicebus.TopicManagementOption) (*servicebus.TopicEntity, error) {
	tm := namespace.NewTopicManager()
	te, err := tm.Get(ctx, name)
	if err == nil {
		return te, nil
	}

	return tm.Put(ctx, name, opts...)
}
