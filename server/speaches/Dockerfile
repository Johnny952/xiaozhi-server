FROM ghcr.io/speaches-ai/speaches:0.8.3-cpu
COPY --chmod=755 preload-entrypoint.sh /preload-entrypoint.sh
ENTRYPOINT ["/preload-entrypoint.sh"]
# Setting ENTRYPOINT resets the inherited CMD; restate the upstream one.
CMD ["uvicorn", "--factory", "speaches.main:create_app"]
