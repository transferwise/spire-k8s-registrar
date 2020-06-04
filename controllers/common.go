package controllers

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/spiffe/spire/proto/spire/api/registration"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func makeSpiffeIdForNodeName(myId string, nodeName string) string {
	return fmt.Sprintf("%s/%s", myId, nodeName)
}

func ensureSpireEntryDeleted(client registration.RegistrationClient, ctx context.Context, reqLogger logr.Logger, entryId string) error {
	if _, err := client.DeleteEntry(ctx, &registration.RegistrationEntryID{Id: entryId}); err != nil {
		if status.Code(err) != codes.NotFound {
			if status.Code(err) == codes.Internal {
				// Spire server currently returns a 500 if delete fails due to the entry not existing. This is probably a bug.
				// We work around it by attempting to fetch the entry, and if it's not found then all is good.
				if _, err := client.FetchEntry(ctx, &registration.RegistrationEntryID{Id: entryId}); err != nil {
					if status.Code(err) == codes.NotFound {
						reqLogger.V(1).Info("Entry already deleted", "entry", entryId)
						return nil
					}
				}
			}
			return err
		}
	}
	reqLogger.Info("deleted entry", "entry", entryId)
	return nil
}
