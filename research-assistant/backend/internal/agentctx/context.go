// Package agentctx defines typed context keys shared by the scheduler and the
// specialized agents. String keys silently collide and hide missing values;
// these helpers make the contract explicit.
package agentctx

import "context"

type logChannelKey struct{}
type codeChannelKey struct{}
type containerIDKey struct{}

// WithLogChannel attaches the scheduler log channel consumed by agents.
func WithLogChannel(ctx context.Context, channel chan string) context.Context {
	return context.WithValue(ctx, logChannelKey{}, channel)
}

// LogChannel returns the attached log channel, if any.
func LogChannel(ctx context.Context) (chan string, bool) {
	if ctx == nil {
		return nil, false
	}
	channel, ok := ctx.Value(logChannelKey{}).(chan string)
	return channel, ok
}

// CodeChannel returns the attached code channel, if any.
func CodeChannel(ctx context.Context) (chan string, bool) {
	if ctx == nil {
		return nil, false
	}
	channel, ok := ctx.Value(codeChannelKey{}).(chan string)
	return channel, ok
}

// ContainerID returns the attached sandbox container id.
func ContainerID(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(containerIDKey{}).(string)
	return id, ok
}
