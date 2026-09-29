package cmd

import (
	"context"

	"github.com/SeventhingsCompany/customer-api-go/client"
	"github.com/SeventhingsCompany/customer-api-go/models"
	"github.com/spf13/cobra"
)

func (a *App) roomsCmd() *cobra.Command {
	return a.fieldResourceCmd(fieldResource{
		use: "rooms", singular: "room", aliases: []string{"room"},
		list:   (*client.Client).RoomsList,
		all:    (*client.Client).RoomsAll,
		count:  (*client.Client).RoomsCount,
		get:    (*client.Client).RoomGet,
		create: (*client.Client).RoomCreate,
		update: (*client.Client).RoomPatch,
		delete: (*client.Client).RoomDelete,
		history: func(c *client.Client, ctx context.Context, uuid string, o *models.HistoryListOptions) (any, error) {
			return c.RoomHistory(ctx, uuid, o)
		},
	})
}

func (a *App) locationsCmd() *cobra.Command {
	return a.fieldResourceCmd(fieldResource{
		use: "locations", singular: "location", aliases: []string{"location", "loc"},
		list:   (*client.Client).LocationsList,
		all:    (*client.Client).LocationsAll,
		count:  (*client.Client).LocationsCount,
		get:    (*client.Client).LocationGet,
		create: (*client.Client).LocationCreate,
		update: (*client.Client).LocationPatch,
		delete: (*client.Client).LocationDelete,
		history: func(c *client.Client, ctx context.Context, uuid string, o *models.HistoryListOptions) (any, error) {
			return c.LocationHistory(ctx, uuid, o)
		},
	})
}
