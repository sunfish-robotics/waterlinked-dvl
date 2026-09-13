package dvl_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"

	dvl "github.com/sunfish-robotics/waterlinked-dvl"
)

func ExampleConn_VelocityReports() {
	ctx := context.Background()
	address := fmt.Sprintf("192.168.194.95:%d", dvl.DefaultPort)

	conn, err := dvl.Dial(ctx, address)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	for sample := range conn.VelocityReports() {
		if sample.DroppedBefore != 0 {
			log.Printf("DVL velocity consumer fell behind: dropped %d reports", sample.DroppedBefore)
		}

		report := sample.Report
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
	}

	if err := conn.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("DVL connection ended: %v", err)
	}
}

func ExampleConn_UpdateConfig() {
	ctx := context.Background()
	address := fmt.Sprintf("192.168.194.95:%d", dvl.DefaultPort)

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

func ExampleConn() {
	ctx := context.Background()
	address := fmt.Sprintf("192.168.194.95:%d", dvl.DefaultPort)

	conn, err := dvl.Dial(ctx, address)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	velocityReports := conn.VelocityReports()
	deadReckoningReports := conn.DeadReckoningReports()
	unknownReports := conn.UnknownReports()
	for {
		select {
		case sample, ok := <-velocityReports:
			if !ok {
				velocityReports = nil
				continue
			}
			log.Printf("Velocity: %v", sample.Report)
		case sample, ok := <-deadReckoningReports:
			if !ok {
				deadReckoningReports = nil
				continue
			}
			log.Printf("Dead reckoning: %v", sample.Report)
		case sample, ok := <-unknownReports:
			if !ok {
				unknownReports = nil
				continue
			}
			log.Printf("Unknown report: %v", sample.Report)
		case <-conn.Done():
			if err := conn.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("DVL connection ended: %v", err)
			}
			return
		}
	}
}
