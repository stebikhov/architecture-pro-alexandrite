package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

type OrderResponse struct {
	OrderID  string  `json:"orderId"`
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

type PriceResponse struct {
	Price float64 `json:"price"`
}

func initProvider() (*sdktrace.TracerProvider, error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "order-service"
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
	r.Use(otelgin.Middleware("order-service"))

	tracer := otel.Tracer("order-service")

	pricingServiceURL := os.Getenv("PRICING_SERVICE_URL")
	if pricingServiceURL == "" {
		pricingServiceURL = "http://pricing-service:8080"
	}

	r.GET("/orders/:orderId", func(c *gin.Context) {
		ctx := c.Request.Context()
		orderID := c.Param("orderId")

		ctx, span := tracer.Start(ctx, "process-order",
			trace.WithAttributes(
				attribute.String("order.id", orderID),
			),
		)
		defer span.End()

		price, err := fetchPrice(ctx, orderID, pricingServiceURL)
		if err != nil {
			span.RecordError(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch price"})
			return
		}

		span.SetAttributes(
			attribute.Float64("order.price", price),
		)

		response := OrderResponse{
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

	log.Printf("Order service starting on port %s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func fetchPrice(ctx context.Context, orderID string, baseURL string) (float64, error) {
	_, span := otel.Tracer("order-service").Start(ctx, "call-pricing-service")
	defer span.End()

	url := fmt.Sprintf("%s/price/%s", baseURL, orderID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return 0, err
	}

	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(req.Header))

	client := &http.Client{}

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var priceResp PriceResponse
	if err := json.Unmarshal(body, &priceResp); err != nil {
		return 0, err
	}

	span.SetAttributes(
		attribute.Float64("price.value", priceResp.Price),
	)

	return priceResp.Price, nil
}
