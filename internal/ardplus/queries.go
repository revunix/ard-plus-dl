// Package ardplus is the ARD Plus API client: the GraphQL metadata API, the
// playback authorization API and the Tatort category pages.
package ardplus

const (
	GraphQLURL  = "https://data.ardplus.de/ard/graphql"
	TokenURL    = "https://token.ardplus.de"
	CategoryURL = "https://www.ardplus.de/kategorie"
)

// GraphQL operations use the persisted-query protocol published by ARD Plus.
const (
	operationMovieDetails  = "MovieDetails"
	operationEpisodes      = "EpisodesInSeasonData"
	movieDetailsHash       = "b84f6a7001135d9c5af93441657145372aba540e4e61bbd8098dda69c060870e"
	episodesInSeasonHash   = "33e68739ed1344c3f791f0c66b7ce2b73d759f7b3897cae7967af724f9231795"
	graphQLMaxAttempts     = 5
	graphQLRetryBaseSecond = 1
)

// episodesInSeasonQuery is the full EpisodesInSeasonData document as the web
// client prints it, including Apollo's added __typename fields. It is sent
// once when the server reports that it does not know the persisted hash.
const episodesInSeasonQuery = `
query EpisodesInSeasonData($seasonId: Guid!) {
  episodes: allCmsEpisodes(
    filter: { seasonId: { equalTo: $seasonId } }
    orderBy: EPISODE_IN_SEASON_ASC
  ) {
    nodes {
      ...EpisodeDetailSliderData
      __typename
    }
    __typename
  }
}

fragment EpisodeDetailSliderData on CmsEpisode {
  id
  episodeInSeason
  title
  runtimeInSeconds
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  fskRating
  flags
  licenses: cmsLicensesByContentEpisodeId(
    filter: { isActive: { equalTo: true } }
  ) {
    nodes {
      properties
      __typename
    }
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  videoSource {
    hlsUrl
    dashUrl
    dashContentId
    pmdUrl
    __typename
  }
  dashAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }
  dashTextTrackInfo {
    language
    isForced
    type
    __typename
  }
  __typename
}

fragment ImageData on CmsImage {
  masterUrl
  width
  height
  name
  __typename
}
`

// movieDetailsQuery is the full MovieDetails document as the web client sends
// it, including Apollo's added __typename fields. It is sent once when the
// server reports that it does not know the persisted hash.
const movieDetailsQuery = `
query MovieDetails(
  $movieId: String!
  $externalId: String!
  $slug: String!
  $potentialMovieId: String!
) {
  movie: getMovieDetails(
    idList: [$movieId, $potentialMovieId, $externalId, $slug]
  ) {
    ...MovieDetailData
    __typename
  }
  series: getSeriesDetails(
    idList: [$movieId, $potentialMovieId, $externalId, $slug]
  ) {
    ...SeriesDetailData
    __typename
  }
}

fragment ImageData on CmsImage {
  masterUrl
  width
  height
  name
  __typename
}

fragment SeriesDetailData on CmsSery {
  id
  externalIdentifier
  slug
  title
  slug
  productionYear
  fskRating
  flags
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  copyright
  rating

  runtimeInSeconds
  productionCountry

  originalTitle
  originalLanguage
  customData
  isPurchasable
  isRentable
  isStreamable
  licenses: cmsLicensesByContentSeriesId(
    filter: { isActive: { equalTo: true } }
  ) {
    nodes {
      properties
      __typename
    }
    __typename
  }
  movieProducts: cmsMovieContentProductsByContentSeriesId {
    nodes {
      isActive
      product {
        paymentProvider
        price
        currencyCode
        type
        available
        description
        qualityLevel
        description
        __typename
      }
      id
      __typename
    }
    __typename
  }
  dashTextTrackInfo {
    language
    isForced
    __typename
  }
  hlsTextTrackInfo {
    language
    isForced
    __typename
  }
  dashAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }
  hlsAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }

  logo {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  headerImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  headerImage24By9 {
    ...ImageData
    __typename
  }

  cast: cmsMovieContentPeopleByContentSeriesId(
    condition: { connectionType: "actor" }
  ) {
    nodes {
      roleName
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  writers: cmsMovieContentPeopleByContentSeriesId(
    condition: { connectionType: "writer" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }

  directors: cmsMovieContentPeopleByContentSeriesId(
    condition: { connectionType: "director" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  seoTags {
    tag {
      label
      __typename
    }
    __typename
  }
  seoTitle
  seoDescription
  seoSharingImage {
    ...ImageData
    __typename
  }

  videoSource {
    dashUrl
    dashContentId
    hlsUrl
    pmdUrl
    __typename
  }

  seasons: cmsSeasonsBySeriesId(orderBy: SEASON_IN_SERIES_ASC) {
    nodes {
      ...SeasonDetailData
      __typename
    }
    __typename
  }

  clips: cmsClipsByParentSeriesId {
    nodes {
      ...ClipData
      __typename
    }
    __typename
  }

  categories: cmsMovieContentCategoriesByContentSeriesId {
    nodes {
      category {
        id
        title
        slug
        fskRating
        parentId
        parent {
          id
          slug
          __typename
        }
        __typename
      }
      __typename
    }
    __typename
  }
  __typename
}

fragment SeasonDetailData on CmsSeason {
  id
  title
  slug
  seasonInSeries

  productionYear
  fskRating
  flags
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  copyright
  rating

  runtimeInSeconds
  productionYear
  productionCountry
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  headerImage {
    ...ImageData
    __typename
  }
  headerImage24By9 {
    ...ImageData
    __typename
  }
  cast: cmsMovieContentPeopleByContentSeasonId(
    condition: { connectionType: "actor" }
  ) {
    nodes {
      roleName
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  writers: cmsMovieContentPeopleByContentSeasonId(
    condition: { connectionType: "writer" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }

  directors: cmsMovieContentPeopleByContentSeasonId(
    condition: { connectionType: "director" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  seoTags {
    tag {
      label
      __typename
    }
    __typename
  }
  # required for directly link via a play button
  firstEpisode: cmsEpisodesBySeasonId(
    first: 1
    orderBy: EPISODE_IN_SEASON_ASC
  ) {
    nodes {
      displayAudioLanguages
      productionYear
      id
      dashAudioInfo {
        language
        isStereo
        isSurround
        type
        __typename
      }
      hlsAudioInfo {
        language
        isStereo
        isSurround
        type
        __typename
      }
      __typename
    }
    __typename
  }
  __typename
}

fragment MovieDetailData on CmsMovie {
  id
  externalIdentifier
  slug
  title
  slug
  productionYear
  fskRating
  flags
  longSynopsis
  mediumSynopsis
  shortSynopsis
  supershortSynopsis
  copyright
  rating
  runtimeInSeconds
  productionCountry
  originalTitle
  originalLanguage
  customData
  isPurchasable
  isRentable
  isStreamable
  movieProducts: cmsMovieContentProductsByContentMovieId {
    nodes {
      isActive
      product {
        paymentProvider
        price
        currencyCode
        type
        available
        description
        qualityLevel
        description
        __typename
      }
      id
      __typename
    }
    __typename
  }
  displayAudioLanguages
  dashTextTrackInfo {
    language
    isForced
    type
    __typename
  }
  hlsTextTrackInfo {
    language
    isForced
    type
    __typename
  }
  dashAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }
  hlsAudioInfo {
    language
    isStereo
    isSurround
    type
    __typename
  }

  logo {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  headerImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  headerImage24By9 {
    ...ImageData
    __typename
  }

  cast: cmsMovieContentPeopleByContentMovieId(
    condition: { connectionType: "actor" }
  ) {
    nodes {
      roleName
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  writers: cmsMovieContentPeopleByContentMovieId(
    condition: { connectionType: "writer" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }

  directors: cmsMovieContentPeopleByContentMovieId(
    condition: { connectionType: "director" }
  ) {
    nodes {
      person {
        name
        __typename
      }
      __typename
    }
    __typename
  }
  seoTags {
    tag {
      label
      __typename
    }
    __typename
  }
  seoTitle
  seoDescription
  seoSharingImage {
    ...ImageData
    __typename
  }

  videoSource {
    dashUrl
    dashContentId
    hlsUrl
    pmdUrl
    __typename
  }

  clips: cmsClipsByParentMovieId {
    nodes {
      ...ClipData
      __typename
    }
    __typename
  }
  trailers: cmsTrailersByParentMovieId {
    nodes {
      ...TrailerData
      __typename
    }
    __typename
  }

  categories: cmsMovieContentCategoriesByContentMovieId {
    nodes {
      category {
        id
        title
        slug
        fskRating
        parentId
        parent {
          id
          slug
          __typename
        }
        __typename
      }
      __typename
    }
    __typename
  }
  __typename
}

fragment ClipData on CmsClip {
  id
  title

  supershortSynopsis
  shortSynopsis
  mediumSynopsis
  longSynopsis
  runtimeInSeconds
  fskRating
  flags

  videoSource {
    hlsUrl
    dashUrl
    dashContentId
    pmdUrl
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  __typename
}

fragment TrailerData on CmsTrailer {
  id
  title

  supershortSynopsis
  shortSynopsis
  mediumSynopsis
  longSynopsis
  runtimeInSeconds
  fskRating
  flags

  videoSource {
    hlsUrl
    dashUrl
    dashContentId
    pmdUrl
    __typename
  }
  widescreenImage {
    ...ImageData
    __typename
  }
  widescreenImageTextless {
    ...ImageData
    __typename
  }
  coverImage {
    ...ImageData
    __typename
  }
  squareImage {
    ...ImageData
    __typename
  }
  __typename
}
`

// CustomData carries the Tatort specific fields. The API sends them either as
// an object or as a JSON string containing that object, so both forms are
// accepted.
