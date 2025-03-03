// Copyright Dose de Telemetria GmbH
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"

	"github.com/dosedetelemetria/projeto-otel-na-pratica/internal/app"
	"github.com/dosedetelemetria/projeto-otel-na-pratica/internal/config"
	"github.com/dosedetelemetria/projeto-otel-na-pratica/internal/telemetry"
	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log/global"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
)

func main() {
	configFlag := flag.String("config", "", "path to the config file")
	otelconfigFlag := flag.String("otel", "otel.yaml", "path to the otel config file")
	flag.Parse()

	closer, err := telemetry.Setup(context.Background(), *otelconfigFlag)
	if err != nil {
		fmt.Printf("Failed to setup telemetry: %v\n", err)
	}
	defer closer(context.Background())

	_, span := otel.Tracer("all-in-one").Start(context.Background(), "main")

	core := zapcore.NewTee(
		zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(os.Stdout), zapcore.InfoLevel),
		otelzap.NewCore("all-in-one", otelzap.WithLoggerProvider(global.GetLoggerProvider())),
	)
	logger := zap.New(core)

	logger.Info("Starting the all-in-one service")
	span.AddEvent("Starting the all-in-one service")
	c, _ := config.LoadConfig(*configFlag)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.Fatal("failed to load the config", zap.Error(err))
	}

	mux := http.NewServeMux()

	// starts the gRPC server
	lis, err := net.Listen("tcp", c.Server.Endpoint.GRPC)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		logger.Fatal("failed to listen the config", zap.Error(err))
	}

	var opts []grpc.ServerOption
	grpcServer := grpc.NewServer(opts...)

	{
		logger.Info("Starting the user service")
		span.AddEvent("Starting the user service")
		a := app.NewUser(&c.Users)
		a.RegisterRoutes(mux)
	}

	{
		logger.Info("Starting the plan service")
		span.AddEvent("Starting the plan service")
		a := app.NewPlan(&c.Plans)
		a.RegisterRoutes(mux, grpcServer)
	}

	{
		logger.Info("Starting the payment service")
		span.AddEvent("Starting the payment service")
		a, err := app.NewPayment(&c.Payments)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			logger.Fatal("failed to create the payment service", zap.Error(err))
		}
		a.RegisterRoutes(mux)
		defer func() {
			logger.Info("Shutting down the payment service")
			err = a.Shutdown()
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				logger.Fatal("Failed to shutdown the payment service", zap.Error(err))
			}
		}()
	}

	{
		logger.Info("Starting the subscriptions service")
		span.AddEvent("Starting the subscriptions service")
		a := app.NewSubscription(&c.Subscriptions)
		a.RegisterRoutes(mux)
	}

	go func() {
		err = grpcServer.Serve(lis)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			logger.Fatal("failed to server", zap.Error(err))
		}
	}()

	span.End()
	err = http.ListenAndServe(c.Server.Endpoint.HTTP, mux)
	if err != nil && err != http.ErrServerClosed {
		logger.Error("failed to serve", zap.Error(err))
	}

	logger.Info("Stopping the all-i-one service")
}
