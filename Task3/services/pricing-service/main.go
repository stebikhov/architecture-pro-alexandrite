package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

type PriceResponse struct {
	OrderID  string  `json:"orderId"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

func initProvider() (*sdktrace.TracerProvider, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "pricing-service"
	}

	jaegerEndpoint := os.Getenv("JAEGER_ENDPOINT")
	if jaegerEndpoint == "" {
		jaegerEndpoint = "http://localhost:14268/api/traces"
	}

	exporter, err := jaeger.New(jaeger.WithCollectorEndpoint(
		jaeger.WithEndpoint(jaegerEndpoint),
	))
	if err != nil {
		log.Printf("Warning: failed to create Jaeger exporter (running without tracing): %v", err)
		res, _ := resource.New(context.Background(),
			resource.WithAttributes(
				semconv.ServiceName(serviceName),
			),
		)
		return sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
		), nil
	}

	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(res),
	)
	return tp, nil
}

func main() {
	tp, err := initProvider()
	if err != nil {
		log.Fatalf("Failed to initialize tracing: %v", err)
	}
	defer func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}
	}()

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	r := gin.Default()
	r.Use(otelgin.Middleware("pricing-service"))

	tracer := otel.Tracer("pricing-service")

	r.GET("/price/:orderId", func(c *gin.Context) {
		ctx := c.Request.Context()
		orderID := c.Param("orderId")

		_, span := tracer.Start(ctx, "calculate-price",
			trace.WithAttributes(
				attribute.String("order.id", orderID),
			),
		)
		defer span.End()

		price := calculatePrice(orderID)

		span.SetAttributes(
			attribute.Float64("price.amount", price),
		)

		response := PriceResponse{
			OrderID:  orderID,
			Price:    price,
			Currency: "RUB",
		}

		c.JSON(http.StatusOK, response)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Pricing service starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func calculatePrice(orderID string) float64 {
	hash := 0
	for _, c := range orderID {
		hash = hash*31 + int(c)
	}
	if hash < 0 {
		hash = -hash
	}
	return float64(hash%50000+1000) / 100
}
