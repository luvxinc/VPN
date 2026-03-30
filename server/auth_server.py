"""WeiAi VPN — Auth Server entry point."""
import asyncio
import ssl
from contextlib import asynccontextmanager
from pathlib import Path

import asyncpg
import redis.asyncio as aioredis
from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse
from fastapi.templating import Jinja2Templates

import config_loader
from routers import auth, admin
from services import clash_poller, log_manager
from version import VERSION

BASE_DIR = Path(__file__).parent


@asynccontextmanager
async def lifespan(app: FastAPI):
    cfg = config_loader.get()

    # PostgreSQL connection pool
    app.state.db = await asyncpg.create_pool(
        cfg["database"]["url"],
        min_size=2,
        max_size=cfg["database"]["pool_size"],
    )

    # Redis client
    app.state.redis = aioredis.from_url(
        cfg["redis"]["url"],
        decode_responses=True,
    )

    app.state.cfg = cfg
    app.state.templates = Jinja2Templates(directory=str(BASE_DIR / "templates"))

    # Background tasks
    poller_task = asyncio.create_task(clash_poller.run(app))
    cleanup_task = asyncio.create_task(log_manager.cleanup_loop(app))

    yield

    # Shutdown
    poller_task.cancel()
    cleanup_task.cancel()
    await app.state.db.close()
    await app.state.redis.aclose()


app = FastAPI(
    title="WeiAi VPN",
    docs_url=None,
    redoc_url=None,
    openapi_url=None,
    lifespan=lifespan,
)

app.include_router(auth.router)
app.include_router(admin.router)


@app.get("/health")
async def health():
    return {"status": "ok", "version": VERSION}


@app.get("/download/client")
async def download_client():
    """Serve the latest macOS client zip for users who need to upgrade."""
    cfg = config_loader.get()
    zip_path = cfg.get("client", {}).get("client_zip_path", "../client/dist/为爱鼓掌.zip")
    if not Path(zip_path).is_absolute():
        zip_path = str(BASE_DIR / zip_path)
    if not Path(zip_path).exists():
        raise HTTPException(404, "Client package not available. Contact your administrator.")
    return FileResponse(
        zip_path,
        media_type="application/zip",
        filename="为爱鼓掌.zip",
    )


if __name__ == "__main__":
    import uvicorn

    cfg = config_loader.get()
    cert = BASE_DIR / cfg["certs"]["cert_path"]
    key = BASE_DIR / cfg["certs"]["key_path"]

    uvicorn.run(
        "auth_server:app",
        host="0.0.0.0",
        port=9443,
        ssl_certfile=str(cert),
        ssl_keyfile=str(key),
        reload=False,
        log_level="info",
    )
