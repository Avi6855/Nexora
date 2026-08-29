@file:Suppress("OVERRIDE_NULLABILITY_MISMATCH")

package com.nexora.app.core.network

import com.google.gson.Gson
import com.google.gson.GsonBuilder
import com.google.gson.TypeAdapter
import com.google.gson.reflect.TypeToken
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.ResponseBody
import retrofit2.Converter
import retrofit2.Retrofit
import java.lang.reflect.Type

class GsonConverterFactory private constructor(private val gson: Gson) : Converter.Factory() {

    override fun requestBodyConverter(
        type: Type,
        parameterAnnotations: Array<out Annotation>,
        methodAnnotations: Array<out Annotation>,
        retrofit: Retrofit
    ): Converter<*, RequestBody> {
        val adapter: TypeAdapter<*> = gson.getAdapter(TypeToken.get(type))
        return GsonRequestBodyConverter(gson, adapter)
    }

    override fun responseBodyConverter(
        type: Type,
        annotations: Array<out Annotation>,
        retrofit: Retrofit
    ): Converter<ResponseBody, *> {
        val adapter: TypeAdapter<*> = gson.getAdapter(TypeToken.get(type))
        return GsonResponseBodyConverter(gson, adapter)
    }

    private class GsonRequestBodyConverter<T>(
        private val gson: Gson,
        private val adapter: TypeAdapter<T>
    ) : Converter<T, RequestBody> {
        override fun convert(value: T): RequestBody {
            val json = adapter.toJson(value)
            return json.toRequestBody("application/json; charset=utf-8".toMediaType())
        }
    }

    private class GsonResponseBodyConverter<T>(
        private val gson: Gson,
        private val adapter: TypeAdapter<T>
    ) : Converter<ResponseBody, T?> {
        override fun convert(value: ResponseBody): T? {
            val json = value.string()
            return adapter.fromJson(json)
        }
    }

    companion object {
        fun create(): GsonConverterFactory {
            val gson = GsonBuilder().create()
            return GsonConverterFactory(gson)
        }

        fun create(gson: Gson): GsonConverterFactory {
            return GsonConverterFactory(gson)
        }
    }
}
