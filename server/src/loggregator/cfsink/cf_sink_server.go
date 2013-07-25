package cfsink

import (
	"code.google.com/p/gogoprotobuf/proto"
	"fmt"
	"github.com/cloudfoundry/gosteno"
	"logMessage"
	"net/http"
	"net/url"
	"regexp"
)

type cfSinkServer struct {
	logger           *gosteno.Logger
	dataChannel      chan []byte
	listenHost       string
	listenPath       string
	apiHost          string
	listenerChannels *groupedChannels
	authorize        LogAccessAuthorizer
}

func NewCfSinkServer(givenChannel chan []byte, logger *gosteno.Logger, listenHost string, listenPath string, apiHost string, authorize LogAccessAuthorizer) *cfSinkServer {
	listeners := newGroupedChannels()
	return &cfSinkServer{logger, givenChannel, listenHost, listenPath, apiHost, listeners, authorize}
}

func (cfSinkServer *cfSinkServer) sinkRelayHandler(rw http.ResponseWriter, r *http.Request) {
	extractAppIdAndAuthTokenFromUrl := func(u *url.URL) (string, string, string) {
		authorization := ""
		queryValues := u.Query()
		if len(queryValues["authorization"]) == 1 {
			authorization = queryValues["authorization"][0]
		}
		appId := ""
		spaceId := ""
		re := regexp.MustCompile("^" + cfSinkServer.listenPath + "spaces/([^/]+)(?:/apps/([^/]+))?$")
		result := re.FindStringSubmatch(u.Path)

		switch len(result) {
		case 2:
			spaceId = result[1]
		case 3:
			spaceId = result[1]
			appId = result[2]
		}

		return spaceId, appId, authorization
	}

	f, ok := rw.(http.Flusher)
	if !ok {
		cfSinkServer.logger.Fatalf("Response writer is not a flusher.")
	}

	clientAddress := r.RemoteAddr

	spaceId, appId, authToken := extractAppIdAndAuthTokenFromUrl(r.URL)

	if spaceId == "" {
		message := fmt.Sprintf("Did not accept sink connection from %s without spaceId.", clientAddress)
		cfSinkServer.logger.Warn(message)
		rw.WriteHeader(400)
		rw.Write([]byte(message))
		return
	}
	if authToken == "" {
		message := fmt.Sprintf("Did not accept sink connection from %s without authorization.", clientAddress)
		cfSinkServer.logger.Warnf(message)
		rw.WriteHeader(400)
		rw.Write([]byte(message))
		return
	}

	if !cfSinkServer.authorize(cfSinkServer.apiHost, authToken, spaceId, appId, cfSinkServer.logger) {
		message := fmt.Sprintf("User not authorized to access space [%s].", spaceId)
		cfSinkServer.logger.Warn(message)
		rw.WriteHeader(401)
		rw.Write([]byte(message))
		return
	}

	f.Flush()

	newCfSink(spaceId, appId, cfSinkServer, &rw, &f, clientAddress)
}

func (cfSinkServer *cfSinkServer) relayMessagesToAllSinks() {
	extractReceivedSpaceAndAppId := func(data []byte) (string, string) {
		receivedMessage := &logMessage.LogMessage{}
		err := proto.Unmarshal(data, receivedMessage)
		if err != nil {
			cfSinkServer.logger.Debugf("Log message could not be unmarshaled. Dropping it... Error: %v. Data: %v", err, data)
			return "", ""
		}
		return *receivedMessage.SpaceId, *receivedMessage.AppId
	}

	for {
		data := <-cfSinkServer.dataChannel
		cfSinkServer.logger.Debugf("Received %d bytes of data from agent listener.", len(data))
		receivedSpaceId, receivedAppId := extractReceivedSpaceAndAppId(data)
		cfSinkServer.logger.Debugf("Searching for channels with spaceId [%s] and appId [%s].", receivedSpaceId, receivedAppId)
		for _, listenerChannel := range cfSinkServer.listenerChannels.get(receivedSpaceId, receivedAppId) {
			cfSinkServer.logger.Debugf("Sending Message to channel %s for space [%s] and app [%s].", listenerChannel, receivedSpaceId, receivedAppId)
			listenerChannel <- data
		}
		cfSinkServer.logger.Debugf("Searching for channels with spaceId [%s].", receivedSpaceId)
		for _, listenerChannel := range cfSinkServer.listenerChannels.get(receivedSpaceId) {
			cfSinkServer.logger.Debugf("Sending Message to channel %s for space [%s].", listenerChannel, receivedSpaceId)
			listenerChannel <- data
		}
		cfSinkServer.logger.Debugf("Done sending message to tail clients.")
	}
}

func (cfSinkServer *cfSinkServer) Start() {
	go cfSinkServer.relayMessagesToAllSinks()
	cfSinkServer.logger.Infof("Listening on port %s", cfSinkServer.listenHost)
	http.HandleFunc(cfSinkServer.listenPath, cfSinkServer.sinkRelayHandler)
	err := http.ListenAndServe(cfSinkServer.listenHost, nil)
	if err != nil {
		panic(err)
	}
}