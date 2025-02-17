package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/amir20/dozzle/docker"

	log "github.com/sirupsen/logrus"
)

func (h *handler) streamEvents(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-transform")
	w.Header().Add("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	events, err := h.client.Events(ctx)
	stats := make(chan docker.ContainerStat)

	if containers, err := h.client.ListContainers(); err == nil {
		for _, c := range containers {
			if c.State == "running" {
				if err := h.client.ContainerStats(ctx, c.ID, stats); err != nil {
					log.Errorf("error while streaming container stats: %v", err)
				}
			}
		}
	}

	if err := sendContainersJSON(h.client, w); err != nil {
		log.Errorf("error while encoding containers to stream: %v", err)
	}

	f.Flush()

	for {
		select {
		case stat := <-stats:
			bytes, _ := json.Marshal(stat)
			if _, err := fmt.Fprintf(w, "event: container-stat\ndata: %s\n\n", string(bytes)); err != nil {
				log.Errorf("error writing stat to event stream: %v", err)
				return
			}
			f.Flush()
		case event, ok := <-events:
			if !ok {
				return
			}
			switch event.Name {
			case "start", "die":
				log.Debugf("triggering docker event: %v", event.Name)
				if event.Name == "start" {
					log.Debugf("found new container with id: %v", event.ActorID)
					if err := h.client.ContainerStats(ctx, event.ActorID, stats); err != nil {
						log.Errorf("error when streaming new container stats: %v", err)
					}
					if err := sendContainersJSON(h.client, w); err != nil {
						log.Errorf("error encoding containers to stream: %v", err)
						return
					}
				}

				bytes, _ := json.Marshal(event)
				if _, err := fmt.Fprintf(w, "event: container-%s\ndata: %s\n\n", event.Name, string(bytes)); err != nil {
					log.Errorf("error writing event to event stream: %v", err)
					return
				}

				f.Flush()
			default:
				// do nothing
			}
		case <-ctx.Done():
			return
		case <-err:
			return
		}
	}
}

func sendContainersJSON(client docker.Client, w http.ResponseWriter) error {
	containers, err := client.ListContainers()
	if err != nil {
		return err
	}

	if _, err := fmt.Fprint(w, "event: containers-changed\ndata: "); err != nil {
		return err
	}

	if err := json.NewEncoder(w).Encode(containers); err != nil {
		return err
	}

	if _, err := fmt.Fprint(w, "\n\n"); err != nil {
		return err
	}

	return nil
}
