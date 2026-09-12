package dvl_test

import (
	"context"
	"errors"
	"log"
	"net"
	"strconv"

	dvl "github.com/sunfish-robotics/waterlinked-dvl"
)

func ExampleConn_Reports() {
	ctx := context.Background()
	address := net.JoinHostPort("192.168.194.95", strconv.Itoa(dvl.DefaultPort))

	conn, err := dvl.Dial(ctx, address)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	for sample := range conn.Reports() {
		if sample.DroppedBefore != 0 {
			log.Printf("DVL report consumer fell behind: dropped %d reports", sample.DroppedBefore)
		}

		switch report := sample.Report.(type) {
		case *dvl.VelocityReport:
			if !report.Valid {
				continue
			}
			log.Printf(
				"%s-relative velocity: x=%.3f y=%.3f z=%.3f m/s",
				report.Reference,
				report.Velocity.X,
				report.Velocity.Y,
				report.Velocity.Z,
			)
		case *dvl.DeadReckoningReport:
			log.Printf(
				"local position: x=%.2f y=%.2f z=%.2f m",
				report.Position.X,
				report.Position.Y,
				report.Position.Z,
			)
		case *dvl.UnknownReport:
			log.Printf("unknown DVL report type %q", report.Type)
		}
	}

	if err := conn.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("DVL connection ended: %v", err)
	}
}

func ExampleConn_UpdateConfig() {
	ctx := context.Background()
	address := net.JoinHostPort("192.168.194.95", strconv.Itoa(dvl.DefaultPort))

	conn, err := dvl.Dial(ctx, address)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	before, err := conn.Config(ctx)
	if err != nil {
		log.Fatal(err)
	}

	desiredSpeedOfSound := 1480.0
	if before.SpeedOfSound != desiredSpeedOfSound {
		err = conn.UpdateConfig(ctx, dvl.ConfigUpdate{
			SpeedOfSound: &desiredSpeedOfSound,
		})
		if err != nil {
			log.Fatal(err)
		}
	}

	after, err := conn.Config(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if after.SpeedOfSound != desiredSpeedOfSound {
		log.Fatalf(
			"DVL accepted speed-of-sound update but reports %.1f m/s",
			after.SpeedOfSound,
		)
	}
}
